import { afterEach, describe, expect, it, vi } from "vitest";
import worker, {
  isStorageRedirect,
  originRequest,
  resolveEdgeManifestDecision,
  storageHeaders,
  type EdgeManifest,
  type Env,
} from "./index";

class MemoryCache {
  private readonly items = new Map<string, Response>();

  async match(request: Request): Promise<Response | undefined> {
    return this.items.get(request.url)?.clone();
  }

  async put(request: Request, response: Response): Promise<void> {
    this.items.set(request.url, response.clone());
  }
}

class HeaderRewritingMemoryCache {
  private readonly items = new Map<string, Response>();

  async match(request: Request): Promise<Response | undefined> {
    const response = this.items.get(request.url)?.clone();
    if (!response) {
      return undefined;
    }
    const out = new Response(response.body, response);
    out.headers.set("Cache-Control", "public, max-age=14400");
    return out;
  }

  async put(request: Request, response: Response): Promise<void> {
    this.items.set(request.url, response.clone());
  }
}

class MemoryKV {
  constructor(private readonly items: Record<string, string>) {}

  async get(key: string): Promise<string | null> {
    return this.items[key] ?? null;
  }
}

function ctx(): ExecutionContext {
  return {
    waitUntil(promise: Promise<unknown>) {
      void promise;
    },
    passThroughOnException() {},
  } as ExecutionContext;
}

function trackedCtx(): { ctx: ExecutionContext; wait: () => Promise<void> } {
  const waits: Promise<unknown>[] = [];
  return {
    ctx: {
      waitUntil(promise: Promise<unknown>) {
        waits.push(promise);
      },
      passThroughOnException() {},
    } as ExecutionContext,
    async wait() {
      await Promise.all(waits);
    },
  };
}

function env(overrides: Partial<Env> = {}): Env {
  return {
    ORIGIN_BASE_URL: "https://origin.example.com",
    EDGE_BYPASS_SECRET: "edge-secret",
    ...overrides,
  };
}

function manifest(overrides: Partial<EdgeManifest> = {}): EdgeManifest {
  return {
    version: 1,
    kind: "supercdn-edge-manifest",
    site_id: "demo",
    deployment_id: "dpl-test",
    mode: "spa",
    routes: {
      "/": {
        type: "origin",
        delivery: "origin",
        file: "index.html",
        status: 200,
        content_type: "text/html; charset=utf-8",
        cache_control: "public, max-age=60",
      },
      "/index.html": {
        type: "origin",
        delivery: "origin",
        file: "index.html",
        status: 200,
      },
      "/assets/app.js": {
        type: "redirect",
        delivery: "redirect",
        file: "assets/app.js",
        status: 302,
        location: "https://storage.example.com/assets/app.js?sign=fresh",
        content_type: "text/javascript; charset=utf-8",
        cache_control: "no-store",
        headers: { "X-Test-Asset": "yes" },
      },
    },
    fallback: {
      type: "origin",
      delivery: "origin",
      file: "index.html",
      status: 200,
    },
    not_found: {
      type: "origin",
      delivery: "redirect",
      file: "404.html",
      status: 404,
    },
    ...overrides,
  };
}

afterEach(() => {
  vi.unstubAllGlobals();
});

describe("edge proxy", () => {
  it("returns manifest dry-run decisions from KV without contacting origin", async () => {
    const fetchSpy = vi.fn(async () => {
      throw new Error("origin should not be called for manifest dry-run");
    });
    vi.stubGlobal("fetch", fetchSpy);

    const key = "sites/site.example.com/active/edge-manifest";
    const res = await worker.fetch(
      new Request("https://site.example.com/assets/app.js?__supercdn_edge_manifest=dry-run"),
      env({
        EDGE_MANIFEST_DRY_RUN: "true",
        EDGE_MANIFEST: new MemoryKV({ [key]: JSON.stringify(manifest()) }) as unknown as KVNamespace,
      }),
      ctx(),
    );

    expect(res.status).toBe(200);
    expect(res.headers.get("X-SuperCDN-Edge-Manifest-Dry-Run")).toBe("true");
    const body = (await res.json()) as {
      ok: boolean;
      key: string;
      decision: {
        action: string;
        route_type: string;
        delivery: string;
        file: string;
        status: number;
        location: string;
        cache_control: string;
        headers: Record<string, string>;
      };
    };
    expect(body.ok).toBe(true);
    expect(body.key).toBe(key);
    expect(body.decision.action).toBe("route");
    expect(body.decision.route_type).toBe("redirect");
    expect(body.decision.delivery).toBe("redirect");
    expect(body.decision.file).toBe("assets/app.js");
    expect(body.decision.status).toBe(302);
    expect(body.decision.location).toBe("https://storage.example.com/assets/app.js?sign=fresh");
    expect(body.decision.cache_control).toBe("no-store");
    expect(body.decision.headers["X-Test-Asset"]).toBe("yes");
    expect(fetchSpy).not.toHaveBeenCalled();
  });

  it("does not enter manifest dry-run unless explicitly enabled", async () => {
    vi.stubGlobal("caches", { default: new MemoryCache() });
    const fetchSpy = vi.fn(async () => new Response("origin", { status: 200 }));
    vi.stubGlobal("fetch", fetchSpy);

    const res = await worker.fetch(
      new Request("https://site.example.com/assets/app.js?__supercdn_edge_manifest=dry-run"),
      env({
        EDGE_MANIFEST: new MemoryKV({
          "sites/site.example.com/active/edge-manifest": JSON.stringify(manifest()),
        }) as unknown as KVNamespace,
      }),
      ctx(),
    );

    expect(res.status).toBe(200);
    expect(await res.text()).toBe("origin");
    expect(fetchSpy).toHaveBeenCalledTimes(1);
  });

  it("follows marked storage redirects and returns same-origin-safe content", async () => {
    const seen: { url: string; headers: Headers }[] = [];
    vi.stubGlobal("caches", { default: new MemoryCache() });
    vi.stubGlobal(
      "fetch",
      vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
        const req = input instanceof Request ? input : new Request(input, init);
        seen.push({ url: req.url, headers: req.headers });
        if (req.url === "https://origin.example.com/assets/app.js") {
          return new Response(null, {
            status: 302,
            headers: {
              Location: "https://storage.example.com/app.js?sign=abc",
              "X-SuperCDN-Redirect": "storage",
              "Cache-Control": "public, max-age=600",
            },
          });
        }
        return new Response("console.log('ok')", {
          status: 200,
          headers: { "Content-Type": "application/octet-stream" },
        });
      }),
    );

    const req = new Request("https://site.example.com/assets/app.js", {
      headers: {
        Cookie: "session=1",
        Authorization: "Bearer secret",
        Referer: "https://site.example.com/",
        Accept: "*/*",
      },
    });
    const res = await worker.fetch(req, env(), ctx());

    expect(res.status).toBe(200);
    expect(res.headers.get("Location")).toBeNull();
    expect(res.headers.get("X-SuperCDN-Edge-Source")).toBe("storage");
    expect(res.headers.get("X-SuperCDN-Cache")).toBe("MISS");
    expect(res.headers.get("Content-Type")).toContain("text/javascript");
    expect(res.headers.get("Cache-Control")).toBe("public, max-age=600");
    expect(await res.text()).toBe("console.log('ok')");

    expect(seen).toHaveLength(2);
    expect(seen[1].url).toBe("https://storage.example.com/app.js?sign=abc");
    expect(seen[1].headers.get("Cookie")).toBeNull();
    expect(seen[1].headers.get("Authorization")).toBeNull();
    expect(seen[1].headers.get("Referer")).toBeNull();
    expect(seen[1].headers.get("Accept")).toBe("*/*");
  });

  it("does not follow normal site redirects", async () => {
    vi.stubGlobal("caches", { default: new MemoryCache() });
    vi.stubGlobal(
      "fetch",
      vi.fn(async () => new Response(null, { status: 301, headers: { Location: "/new" } })),
    );

    const res = await worker.fetch(new Request("https://site.example.com/old"), env(), ctx());

    expect(res.status).toBe(301);
    expect(res.headers.get("Location")).toBe("/new");
  });

  it("falls back to origin streaming when storage fetch fails and the secret is configured", async () => {
    const seen: Request[] = [];
    vi.stubGlobal("caches", { default: new MemoryCache() });
    vi.stubGlobal(
      "fetch",
      vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
        const req = input instanceof Request ? input : new Request(input, init);
        seen.push(req);
        if (req.url === "https://origin.example.com/assets/app.css" && !req.headers.has("X-SuperCDN-Origin-Delivery")) {
          return new Response(null, {
            status: 302,
            headers: {
              Location: "https://storage.example.com/app.css?sign=abc",
              "X-SuperCDN-Redirect": "storage",
            },
          });
        }
        if (req.url.startsWith("https://storage.example.com/")) {
          return new Response("forbidden", { status: 403 });
        }
        return new Response("body{}", { status: 200, headers: { "Content-Type": "text/css" } });
      }),
    );

    const res = await worker.fetch(new Request("https://site.example.com/assets/app.css"), env(), ctx());

    expect(res.status).toBe(200);
    expect(res.headers.get("X-SuperCDN-Edge-Fallback")).toBe("origin");
    expect(await res.text()).toBe("body{}");
    expect(seen[2].headers.get("X-SuperCDN-Origin-Delivery")).toBe("origin");
    expect(seen[2].headers.get("X-SuperCDN-Edge-Secret")).toBe("edge-secret");
  });

  it("bypasses cache for range requests", async () => {
    vi.stubGlobal("caches", { default: new MemoryCache() });
    vi.stubGlobal(
      "fetch",
      vi.fn(async () => new Response("part", { status: 206, headers: { "Content-Range": "bytes 0-3/10" } })),
    );

    const res = await worker.fetch(new Request("https://site.example.com/video.mp4", { headers: { Range: "bytes=0-3" } }), env(), ctx());

    expect(res.status).toBe(206);
    expect(res.headers.get("X-SuperCDN-Cache")).toBe("BYPASS");
  });

  it("preserves response Cache-Control on cache hits", async () => {
    vi.stubGlobal("caches", { default: new HeaderRewritingMemoryCache() });
    vi.stubGlobal(
      "fetch",
      vi.fn(async () => new Response("<html></html>", {
        status: 200,
        headers: {
          "Content-Type": "text/html; charset=utf-8",
          "Cache-Control": "public, max-age=300",
        },
      })),
    );

    const execution = trackedCtx();
    const req = new Request("https://site.example.com/");
    const miss = await worker.fetch(req, env(), execution.ctx);
    expect(miss.headers.get("X-SuperCDN-Cache")).toBe("MISS");
    expect(miss.headers.get("Cache-Control")).toBe("public, max-age=300");
    await execution.wait();

    const hit = await worker.fetch(req, env(), ctx());
    expect(hit.headers.get("X-SuperCDN-Cache")).toBe("HIT");
    expect(hit.headers.get("Cache-Control")).toBe("public, max-age=300");
    expect(hit.headers.get("X-SuperCDN-Cached-Cache-Control")).toBeNull();
  });
});

describe("edge manifest routing", () => {
  it("matches redirects, rewrites, SPA fallback and not_found in Go-origin order", () => {
    const base = manifest({
      rules: {
        redirects: [{ from: "/old", to: "/new", status: 301 }],
        rewrites: [{ from: "/docs/*", to: "/index.html" }],
      },
      not_found: {
        type: "origin",
        delivery: "redirect",
        file: "404.html",
        status: 404,
      },
    });

    expect(resolveEdgeManifestDecision(new Request("https://site.example.com/old"), base)).toMatchObject({
      action: "site_redirect",
      status: 301,
      location: "/new",
      reason: "matched_redirect_rule",
    });
    expect(resolveEdgeManifestDecision(new Request("https://site.example.com/docs/page"), base)).toMatchObject({
      action: "route",
      serve_path: "/index.html",
      file: "index.html",
      reason: "matched_route",
    });
    expect(resolveEdgeManifestDecision(new Request("https://site.example.com/movie/123"), base)).toMatchObject({
      action: "fallback",
      file: "index.html",
      reason: "spa_fallback",
    });

    const standard = manifest({ fallback: undefined });
    expect(resolveEdgeManifestDecision(new Request("https://site.example.com/missing"), standard)).toMatchObject({
      action: "not_found",
      file: "404.html",
      status: 404,
      reason: "not_found",
    });
  });
});

describe("helpers", () => {
  it("detects only marked storage redirects", () => {
    expect(
      isStorageRedirect(
        new Response(null, {
          status: 302,
          headers: { Location: "https://storage.example.com/a", "X-SuperCDN-Redirect": "storage" },
        }),
      ),
    ).toBe(true);
    expect(isStorageRedirect(new Response(null, { status: 302, headers: { Location: "/login" } }))).toBe(false);
  });

  it("builds origin and storage requests with safe headers", () => {
    const req = new Request("https://site.example.com/a.js?x=1", {
      headers: {
        Cookie: "a=b",
        Referer: "https://site.example.com/",
        Range: "bytes=0-1",
      },
    });
    const origin = originRequest(req, env(), true);
    expect(origin.url).toBe("https://origin.example.com/a.js?x=1");
    expect(origin.headers.get("X-Forwarded-Host")).toBe("site.example.com");
    expect(origin.headers.get("X-SuperCDN-Edge-Secret")).toBe("edge-secret");

    const storage = storageHeaders(req);
    expect(storage.get("Range")).toBe("bytes=0-1");
    expect(storage.get("Cookie")).toBeNull();
    expect(storage.get("Referer")).toBeNull();
  });
});
