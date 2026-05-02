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

class MemoryAssets {
  constructor(private readonly handler: (request: Request) => Promise<Response>) {}

  async fetch(input: RequestInfo | URL, init?: RequestInit): Promise<Response> {
    const request = input instanceof Request ? input : new Request(input, init);
    return this.handler(request);
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

  it("routes manifest storage redirects without contacting origin", async () => {
    const fetchSpy = vi.fn(async () => {
      throw new Error("origin should not be called for manifest routes");
    });
    vi.stubGlobal("fetch", fetchSpy);

    const res = await worker.fetch(
      new Request("https://site.example.com/assets/app.js"),
      env({
        EDGE_MANIFEST_MODE: "route",
        EDGE_MANIFEST: new MemoryKV({
          "sites/site.example.com/active/edge-manifest": JSON.stringify(manifest()),
        }) as unknown as KVNamespace,
      }),
      ctx(),
    );

    expect(res.status).toBe(302);
    expect(res.headers.get("Location")).toBe("https://storage.example.com/assets/app.js?sign=fresh");
    expect(res.headers.get("Cache-Control")).toBe("no-store");
    expect(res.headers.get("X-Test-Asset")).toBe("yes");
    expect(res.headers.get("X-SuperCDN-Edge-Manifest")).toBe("route");
    expect(res.headers.get("X-SuperCDN-Edge-Action")).toBe("route");
    expect(res.headers.get("X-SuperCDN-Edge-Source")).toBe("manifest");
    expect(res.headers.get("X-SuperCDN-Edge-File")).toBe("assets/app.js");
    expect(fetchSpy).not.toHaveBeenCalled();
  });

  it("proxies http manifest redirects for https pages", async () => {
    const seen: Request[] = [];
    const fetchSpy = vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
      const req = input instanceof Request ? input : new Request(input, init);
      seen.push(req);
      if (req.url !== "http://storage.example.com/assets/app.js?sign=fresh") {
        throw new Error(`unexpected fetch ${req.url}`);
      }
      return new Response("console.log('proxied')", {
        status: 200,
        headers: {
          "Content-Type": "application/octet-stream",
          "Set-Cookie": "storage=1",
        },
      });
    });
    vi.stubGlobal("fetch", fetchSpy);

    const res = await worker.fetch(
      new Request("https://site.example.com/assets/app.js", {
        headers: {
          Accept: "*/*",
          Cookie: "session=1",
          Range: "bytes=0-10",
        },
      }),
      env({
        EDGE_MANIFEST_MODE: "route",
        EDGE_MANIFEST: new MemoryKV({
          "sites/site.example.com/active/edge-manifest": JSON.stringify(
            manifest({
              routes: {
                "/assets/app.js": {
                  type: "redirect",
                  delivery: "redirect",
                  file: "assets/app.js",
                  status: 302,
                  location: "http://storage.example.com/assets/app.js?sign=fresh",
                  content_type: "text/javascript; charset=utf-8",
                  cache_control: "no-store",
                  headers: { "X-Test-Asset": "yes" },
                },
              },
            }),
          ),
        }) as unknown as KVNamespace,
      }),
      ctx(),
    );

    expect(res.status).toBe(200);
    expect(await res.text()).toBe("console.log('proxied')");
    expect(res.headers.get("Location")).toBeNull();
    expect(res.headers.get("Set-Cookie")).toBeNull();
    expect(res.headers.get("Content-Type")).toBe("text/javascript; charset=utf-8");
    expect(res.headers.get("Cache-Control")).toBe("no-store");
    expect(res.headers.get("X-Test-Asset")).toBe("yes");
    expect(res.headers.get("X-SuperCDN-Edge-Manifest")).toBe("route");
    expect(res.headers.get("X-SuperCDN-Edge-Source")).toBe("storage");
    expect(res.headers.get("X-SuperCDN-Edge-Proxy")).toBe("mixed_content");
    expect(res.headers.get("X-SuperCDN-Edge-File")).toBe("assets/app.js");
    expect(fetchSpy).toHaveBeenCalledTimes(1);
    expect(seen[0].headers.get("Cookie")).toBeNull();
    expect(seen[0].headers.get("Range")).toBe("bytes=0-10");
  });

  it("routes smart manifest candidates by request region", async () => {
    const fetchSpy = vi.fn(async () => {
      throw new Error("origin should not be called for smart manifest routes");
    });
    vi.stubGlobal("fetch", fetchSpy);
    const smart = manifest({
      routing_policy: "global_smart",
      routes: {
        "/assets/app.js": {
          type: "smart",
          delivery: "redirect",
          file: "assets/app.js",
          status: 302,
          location: "https://china.example/app.js?sig=cn",
          cache_control: "no-store",
          routing_policy: {
            name: "global_smart",
            mode: "global_load_balance",
            default_region_group: "overseas",
          },
          candidates: [
            {
              target: "china",
              target_type: "r2",
              type: "redirect",
              region_group: "china",
              weight: 1,
              url: "https://china.example/app.js?sig=cn",
              status: "ready",
            },
            {
              target: "overseas",
              target_type: "r2",
              type: "redirect",
              region_group: "overseas",
              weight: 1,
              url: "https://overseas.example/app.js?sig=global",
              status: "ready",
            },
          ],
        },
      },
    });

    const cn = await worker.fetch(
      new Request("https://site.example.com/assets/app.js", { headers: { "CF-IPCountry": "CN" } }),
      env({
        EDGE_MANIFEST_MODE: "route",
        EDGE_MANIFEST: new MemoryKV({
          "sites/site.example.com/active/edge-manifest": JSON.stringify(smart),
        }) as unknown as KVNamespace,
      }),
      ctx(),
    );
    expect(cn.status).toBe(302);
    expect(cn.headers.get("Location")).toBe("https://china.example/app.js?sig=cn");
    expect(cn.headers.get("X-SuperCDN-Route-Policy")).toBe("global_smart");
    expect(cn.headers.get("X-SuperCDN-Route-Target")).toBe("china");
    expect(cn.headers.get("X-SuperCDN-Route-Reason")).toBe("region_balance:china");

    const us = await worker.fetch(
      new Request("https://site.example.com/assets/app.js", { headers: { "CF-IPCountry": "US" } }),
      env({
        EDGE_MANIFEST_MODE: "route",
        EDGE_MANIFEST: new MemoryKV({
          "sites/site.example.com/active/edge-manifest": JSON.stringify(smart),
        }) as unknown as KVNamespace,
      }),
      ctx(),
    );
    expect(us.status).toBe(302);
    expect(us.headers.get("Location")).toBe("https://overseas.example/app.js?sig=global");
    expect(us.headers.get("X-SuperCDN-Route-Target")).toBe("overseas");
    expect(fetchSpy).not.toHaveBeenCalled();
  });

  it("proxies http smart manifest candidates for https pages", async () => {
    const fetchSpy = vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
      const req = input instanceof Request ? input : new Request(input, init);
      if (req.url !== "http://china.example/app.js") {
        throw new Error(`unexpected fetch ${req.url}`);
      }
      return new Response("console.log('china')", {
        status: 200,
        headers: { "Content-Type": "application/octet-stream" },
      });
    });
    vi.stubGlobal("fetch", fetchSpy);

    const res = await worker.fetch(
      new Request("https://site.example.com/assets/app.js", { headers: { "CF-IPCountry": "CN" } }),
      env({
        EDGE_MANIFEST_MODE: "route",
        EDGE_MANIFEST: new MemoryKV({
          "sites/site.example.com/active/edge-manifest": JSON.stringify(
            manifest({
              routing_policy: "global_smart",
              routes: {
                "/assets/app.js": {
                  type: "smart",
                  delivery: "redirect",
                  file: "assets/app.js",
                  status: 302,
                  content_type: "text/javascript; charset=utf-8",
                  routing_policy: {
                    name: "global_smart",
                    mode: "global_accel",
                    default_region_group: "overseas",
                  },
                  candidates: [
                    { target: "china", type: "redirect", region_group: "china", url: "http://china.example/app.js", status: "ready" },
                    { target: "overseas", type: "redirect", region_group: "overseas", url: "https://overseas.example/app.js", status: "ready" },
                  ],
                },
              },
            }),
          ),
        }) as unknown as KVNamespace,
      }),
      ctx(),
    );

    expect(res.status).toBe(200);
    expect(await res.text()).toBe("console.log('china')");
    expect(res.headers.get("Content-Type")).toBe("text/javascript; charset=utf-8");
    expect(res.headers.get("X-SuperCDN-Edge-Source")).toBe("storage");
    expect(res.headers.get("X-SuperCDN-Route-Policy")).toBe("global_smart");
    expect(res.headers.get("X-SuperCDN-Route-Target")).toBe("china");
    expect(res.headers.get("X-SuperCDN-Route-Reason")).toBe("region:china");
    expect(fetchSpy).toHaveBeenCalledTimes(1);
  });

  it("includes smart routing decisions in manifest dry-run", async () => {
    vi.stubGlobal("fetch", vi.fn(async () => new Response("origin", { status: 200 })));
    const res = await worker.fetch(
      new Request("https://site.example.com/assets/app.js?__supercdn_edge_manifest=dry-run", {
        headers: { "CF-IPCountry": "CN" },
      }),
      env({
        EDGE_MANIFEST_DRY_RUN: "true",
        EDGE_MANIFEST: new MemoryKV({
          "sites/site.example.com/active/edge-manifest": JSON.stringify(
            manifest({
              routing_policy: "global_smart",
              routes: {
                "/assets/app.js": {
                  type: "smart",
                  delivery: "redirect",
                  file: "assets/app.js",
                  status: 302,
                  routing_policy: {
                    name: "global_smart",
                    mode: "global_accel",
                    default_region_group: "overseas",
                  },
                  candidates: [
                    { target: "china", type: "redirect", region_group: "china", url: "https://china.example/app.js", status: "ready" },
                    { target: "overseas", type: "redirect", region_group: "overseas", url: "https://overseas.example/app.js", status: "ready" },
                  ],
                },
              },
            }),
          ),
        }) as unknown as KVNamespace,
      }),
      ctx(),
    );

    expect(res.status).toBe(200);
    const body = (await res.json()) as {
      decision: {
        route_type: string;
        routing_policy: { name: string };
        selected_candidate: { target: string; url: string };
        routing_reason: string;
      };
    };
    expect(body.decision.route_type).toBe("smart");
    expect(body.decision.routing_policy.name).toBe("global_smart");
    expect(body.decision.selected_candidate.target).toBe("china");
    expect(body.decision.routing_reason).toBe("region:china");
  });

  it("proxies manifest IPFS routes through gateway fallbacks", async () => {
    const fetchSpy = vi.fn(async (input: RequestInfo | URL) => {
      const url = input instanceof Request ? input.url : input.toString();
      if (url === "https://gateway-one.example/ipfs/bafywall") {
        return new Response("bad gateway", { status: 502 });
      }
      if (url === "https://gateway-two.example/ipfs/bafywall") {
        return new Response("image-bytes", {
          status: 200,
          headers: {
            "Content-Type": "application/octet-stream",
          },
        });
      }
      throw new Error(`unexpected fetch ${url}`);
    });
    vi.stubGlobal("fetch", fetchSpy);

    const res = await worker.fetch(
      new Request("https://site.example.com/assets/wall.png"),
      env({
        EDGE_MANIFEST_MODE: "route",
        EDGE_MANIFEST: new MemoryKV({
          "sites/site.example.com/active/edge-manifest": JSON.stringify(
            manifest({
              routes: {
                "/assets/wall.png": {
                  type: "ipfs",
                  delivery: "redirect",
                  file: "assets/wall.png",
                  status: 200,
                  location: "https://gateway-one.example/ipfs/bafywall",
                  content_type: "image/png",
                  object_cache_control: "public, max-age=31536000, immutable",
                  gateway_fallbacks: [
                    "https://gateway-one.example/ipfs/bafywall",
                    "https://gateway-two.example/ipfs/bafywall",
                  ],
                  ipfs: [{ cid: "bafywall", provider: "pinata", target: "ipfs_pinata" }],
                },
              },
            }),
          ),
        }) as unknown as KVNamespace,
      }),
      ctx(),
    );

    expect(res.status).toBe(200);
    expect(await res.text()).toBe("image-bytes");
    expect(fetchSpy).toHaveBeenCalledTimes(2);
    expect(res.headers.get("Content-Type")).toBe("image/png");
    expect(res.headers.get("Cache-Control")).toBe("public, max-age=31536000, immutable");
    expect(res.headers.get("X-SuperCDN-Edge-Manifest")).toBe("route");
    expect(res.headers.get("X-SuperCDN-Edge-Source")).toBe("ipfs_gateway");
    expect(res.headers.get("X-SuperCDN-Edge-File")).toBe("assets/wall.png");
  });

  it("proxies explicit resource failover routes without contacting origin", async () => {
    const fetchSpy = vi.fn(async (input: RequestInfo | URL) => {
      const url = input instanceof Request ? input.url : input.toString();
      if (url === "https://primary.example/assets/app.js") {
        return new Response("primary down", { status: 502 });
      }
      if (url === "https://backup.example/assets/app.js") {
        return new Response("console.log('backup')", {
          status: 200,
          headers: { "Content-Type": "application/octet-stream" },
        });
      }
      throw new Error(`unexpected fetch ${url}`);
    });
    vi.stubGlobal("fetch", fetchSpy);

    const res = await worker.fetch(
      new Request("https://site.example.com/assets/app.js"),
      env({
        EDGE_MANIFEST_MODE: "route",
        EDGE_MANIFEST: new MemoryKV({
          "sites/site.example.com/active/edge-manifest": JSON.stringify(
            manifest({
              resource_failover: true,
              routes: {
                "/assets/app.js": {
                  type: "failover",
                  delivery: "failover",
                  file: "assets/app.js",
                  status: 200,
                  content_type: "text/javascript; charset=utf-8",
                  object_cache_control: "public, max-age=300",
                  candidates: [
                    { target: "primary", type: "redirect", priority: 0, url: "https://primary.example/assets/app.js", status: "ready" },
                    { target: "backup", type: "redirect", priority: 1, url: "https://backup.example/assets/app.js", status: "ready" },
                  ],
                },
              },
            }),
          ),
        }) as unknown as KVNamespace,
      }),
      ctx(),
    );

    expect(res.status).toBe(200);
    expect(await res.text()).toBe("console.log('backup')");
    expect(fetchSpy).toHaveBeenCalledTimes(2);
    expect(res.headers.get("Content-Type")).toBe("text/javascript; charset=utf-8");
    expect(res.headers.get("X-SuperCDN-Edge-Source")).toBe("resource_failover");
    expect(res.headers.get("X-SuperCDN-Route-Target")).toBe("backup");
    expect(res.headers.get("X-SuperCDN-Route-Reason")).toBe("resource_failover");
  });

  it("returns an edge error for failover routes with no ready candidates", async () => {
    const fetchSpy = vi.fn(async () => new Response("origin should not be used", { status: 200 }));
    vi.stubGlobal("fetch", fetchSpy);

    const res = await worker.fetch(
      new Request("https://site.example.com/assets/app.js"),
      env({
        EDGE_MANIFEST_MODE: "route",
        EDGE_ORIGIN_FALLBACK: "true",
        EDGE_MANIFEST: new MemoryKV({
          "sites/site.example.com/active/edge-manifest": JSON.stringify(
            manifest({
              resource_failover: true,
              routes: {
                "/assets/app.js": {
                  type: "failover",
                  delivery: "failover",
                  file: "assets/app.js",
                  status: 502,
                  candidates: [],
                },
              },
            }),
          ),
        }) as unknown as KVNamespace,
      }),
      ctx(),
    );

    expect(res.status).toBe(502);
    expect(res.headers.get("X-SuperCDN-Edge-Error")).toBe("resource_failover_failed");
    expect(await res.text()).toContain("no ready failover candidate");
    expect(fetchSpy).not.toHaveBeenCalled();
  });

  it("routes manifest redirects before range bypasses", async () => {
    const fetchSpy = vi.fn(async () => {
      throw new Error("origin should not be called for manifest range redirects");
    });
    vi.stubGlobal("fetch", fetchSpy);

    const res = await worker.fetch(
      new Request("https://site.example.com/assets/app.js", { headers: { Range: "bytes=0-3" } }),
      env({
        EDGE_MANIFEST_MODE: "route",
        EDGE_MANIFEST: new MemoryKV({
          "sites/site.example.com/active/edge-manifest": JSON.stringify(manifest()),
        }) as unknown as KVNamespace,
      }),
      ctx(),
    );

    expect(res.status).toBe(302);
    expect(res.headers.get("Location")).toBe("https://storage.example.com/assets/app.js?sign=fresh");
    expect(fetchSpy).not.toHaveBeenCalled();
  });

  it("routes manifest site redirects without contacting origin", async () => {
    const fetchSpy = vi.fn(async () => {
      throw new Error("origin should not be called for manifest site redirects");
    });
    vi.stubGlobal("fetch", fetchSpy);

    const res = await worker.fetch(
      new Request("https://site.example.com/old"),
      env({
        EDGE_MANIFEST_MODE: "route",
        EDGE_MANIFEST: new MemoryKV({
          "sites/site.example.com/active/edge-manifest": JSON.stringify(
            manifest({ rules: { redirects: [{ from: "/old", to: "/new", status: 301 }] } }),
          ),
        }) as unknown as KVNamespace,
      }),
      ctx(),
    );

    expect(res.status).toBe(301);
    expect(res.headers.get("Location")).toBe("/new");
    expect(res.headers.get("X-SuperCDN-Edge-Manifest")).toBe("route");
    expect(res.headers.get("X-SuperCDN-Edge-Action")).toBe("site_redirect");
    expect(fetchSpy).not.toHaveBeenCalled();
  });

  it("falls back to origin for manifest origin routes in route mode only when explicitly enabled", async () => {
    vi.stubGlobal("caches", { default: new MemoryCache() });
    const fetchSpy = vi.fn(async () => new Response("<html></html>", { status: 200 }));
    vi.stubGlobal("fetch", fetchSpy);

    const res = await worker.fetch(
      new Request("https://site.example.com/"),
      env({
        EDGE_MANIFEST_MODE: "route",
        EDGE_ORIGIN_FALLBACK: "true",
        EDGE_MANIFEST: new MemoryKV({
          "sites/site.example.com/active/edge-manifest": JSON.stringify(manifest()),
        }) as unknown as KVNamespace,
      }),
      ctx(),
    );

    expect(res.status).toBe(200);
    expect(await res.text()).toBe("<html></html>");
    expect(fetchSpy).toHaveBeenCalledTimes(1);
  });

  it("blocks origin fallback for manifest origin routes in route mode by default", async () => {
    const fetchSpy = vi.fn(async () => {
      throw new Error("origin should not be called unless fallback is explicit");
    });
    vi.stubGlobal("fetch", fetchSpy);

    const res = await worker.fetch(
      new Request("https://site.example.com/"),
      env({
        EDGE_MANIFEST_MODE: "route",
        EDGE_MANIFEST: new MemoryKV({
          "sites/site.example.com/active/edge-manifest": JSON.stringify(manifest()),
        }) as unknown as KVNamespace,
      }),
      ctx(),
    );

    expect(res.status).toBe(502);
    expect(res.headers.get("X-SuperCDN-Edge-Manifest")).toBe("route");
    expect(res.headers.get("X-SuperCDN-Edge-Error")).toBe("manifest_direct_route_unavailable");
    expect(fetchSpy).not.toHaveBeenCalled();
  });

  it("blocks origin fallback for unresolved manifest routes in enforce mode", async () => {
    const fetchSpy = vi.fn(async () => {
      throw new Error("origin should not be called in enforce mode");
    });
    vi.stubGlobal("fetch", fetchSpy);

    const res = await worker.fetch(
      new Request("https://site.example.com/"),
      env({
        EDGE_MANIFEST_MODE: "enforce",
        EDGE_MANIFEST: new MemoryKV({
          "sites/site.example.com/active/edge-manifest": JSON.stringify(manifest()),
        }) as unknown as KVNamespace,
      }),
      ctx(),
    );

    expect(res.status).toBe(502);
    expect(res.headers.get("X-SuperCDN-Edge-Manifest")).toBe("route");
    expect(res.headers.get("X-SuperCDN-Edge-Error")).toBe("manifest_direct_route_unavailable");
    expect(fetchSpy).not.toHaveBeenCalled();
  });

  it("serves Cloudflare Static Assets after manifest origin routes without contacting origin", async () => {
    const fetchSpy = vi.fn(async () => {
      throw new Error("origin should not be called when static assets are enabled");
    });
    const assetsFetch = vi.fn(async () => {
      return new Response("<html>static</html>", {
        status: 200,
        headers: {
          "Content-Type": "text/html; charset=utf-8",
          "Cache-Control": "public, max-age=0, must-revalidate",
        },
      });
    });
    vi.stubGlobal("fetch", fetchSpy);

    const res = await worker.fetch(
      new Request("https://site.example.com/"),
      env({
        EDGE_MANIFEST_MODE: "enforce",
        EDGE_STATIC_ASSETS: "true",
        EDGE_MANIFEST: new MemoryKV({
          "sites/site.example.com/active/edge-manifest": JSON.stringify(manifest()),
        }) as unknown as KVNamespace,
        ASSETS: new MemoryAssets(assetsFetch) as unknown as Fetcher,
      }),
      ctx(),
    );

    expect(res.status).toBe(200);
    expect(res.headers.get("X-SuperCDN-Edge-Source")).toBe("cloudflare_static");
    expect(res.headers.get("Cache-Control")).toBe("public, max-age=0, must-revalidate");
    expect(await res.text()).toBe("<html>static</html>");
    expect(assetsFetch).toHaveBeenCalledTimes(1);
    expect(fetchSpy).not.toHaveBeenCalled();
  });

  it("uses Cloudflare Static Assets without requiring an edge manifest", async () => {
    const fetchSpy = vi.fn(async () => {
      throw new Error("origin should not be called for static asset mode");
    });
    vi.stubGlobal("fetch", fetchSpy);

    const res = await worker.fetch(
      new Request("https://site.example.com/movie/123"),
      env({
        EDGE_STATIC_ASSETS: "true",
        ASSETS: new MemoryAssets(async () => new Response("<html>spa</html>", { status: 200 })) as unknown as Fetcher,
      }),
      ctx(),
    );

    expect(res.status).toBe(200);
    expect(res.headers.get("X-SuperCDN-Edge-Source")).toBe("cloudflare_static");
    expect(await res.text()).toBe("<html>spa</html>");
    expect(fetchSpy).not.toHaveBeenCalled();
  });

  it("temporarily falls back entry HTML to origin only when explicitly enabled", async () => {
    const fetchSpy = vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
      const req = input instanceof Request ? input : new Request(input, init);
      if (req.url !== "https://origin.example.com/movie/123") {
        throw new Error(`unexpected origin fetch ${req.url}`);
      }
      return new Response("<html>origin fallback</html>", {
        status: 200,
        headers: { "Content-Type": "text/html; charset=utf-8" },
      });
    });
    vi.stubGlobal("fetch", fetchSpy);

    const res = await worker.fetch(
      new Request("https://site.example.com/movie/123", { headers: { Accept: "text/html" } }),
      env({
        EDGE_STATIC_ASSETS: "true",
        EDGE_ENTRY_ORIGIN_FALLBACK: "true",
        ASSETS: new MemoryAssets(async () => new Response("static down", { status: 503 })) as unknown as Fetcher,
      }),
      ctx(),
    );

    expect(res.status).toBe(200);
    expect(await res.text()).toBe("<html>origin fallback</html>");
    expect(res.headers.get("Cache-Control")).toBe("no-store");
    expect(res.headers.get("X-SuperCDN-Edge-Fallback")).toBe("origin_entry");
    expect(res.headers.get("X-SuperCDN-Edge-Fallback-Reason")).toBe("cloudflare_static_503");
    expect(res.headers.get("X-SuperCDN-Edge-Warning")).toContain("temporary_origin_entry_fallback");
    expect(fetchSpy).toHaveBeenCalledTimes(1);
  });

  it("does not fall back static assets to origin when entry fallback is enabled", async () => {
    const fetchSpy = vi.fn(async () => {
      throw new Error("origin should not be called for static assets");
    });
    vi.stubGlobal("fetch", fetchSpy);

    const res = await worker.fetch(
      new Request("https://site.example.com/assets/app.js", { headers: { Accept: "*/*" } }),
      env({
        EDGE_STATIC_ASSETS: "true",
        EDGE_ENTRY_ORIGIN_FALLBACK: "true",
        ASSETS: new MemoryAssets(async () => new Response("asset down", { status: 503 })) as unknown as Fetcher,
      }),
      ctx(),
    );

    expect(res.status).toBe(503);
    expect(res.headers.get("X-SuperCDN-Edge-Source")).toBe("cloudflare_static");
    expect(res.headers.get("X-SuperCDN-Edge-Fallback")).toBeNull();
    expect(fetchSpy).not.toHaveBeenCalled();
  });

  it("does not send non-GET traffic to origin when static assets are enabled", async () => {
    const fetchSpy = vi.fn(async () => {
      throw new Error("origin should not be called for static asset methods");
    });
    vi.stubGlobal("fetch", fetchSpy);

    const res = await worker.fetch(
      new Request("https://site.example.com/api", { method: "POST" }),
      env({ EDGE_STATIC_ASSETS: "true" }),
      ctx(),
    );

    expect(res.status).toBe(405);
    expect(res.headers.get("Allow")).toBe("GET, HEAD");
    expect(fetchSpy).not.toHaveBeenCalled();
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
