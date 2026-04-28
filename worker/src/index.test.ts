import { afterEach, describe, expect, it, vi } from "vitest";
import worker, { isStorageRedirect, originRequest, storageHeaders, type Env } from "./index";

class MemoryCache {
  private readonly items = new Map<string, Response>();

  async match(request: Request): Promise<Response | undefined> {
    return this.items.get(request.url)?.clone();
  }

  async put(request: Request, response: Response): Promise<void> {
    this.items.set(request.url, response.clone());
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

function env(overrides: Partial<Env> = {}): Env {
  return {
    ORIGIN_BASE_URL: "https://origin.example.com",
    EDGE_BYPASS_SECRET: "edge-secret",
    ...overrides,
  };
}

afterEach(() => {
  vi.unstubAllGlobals();
});

describe("edge proxy", () => {
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
