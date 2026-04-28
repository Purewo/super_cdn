export interface Env {
  ORIGIN_BASE_URL: string;
  EDGE_BYPASS_SECRET?: string;
  EDGE_DEFAULT_CACHE_CONTROL?: string;
}

const cacheableStatus = new Set([200, 404]);
const storageRedirectStatus = new Set([301, 302, 303, 307, 308]);
const hopByHopHeaders = new Set([
  "connection",
  "keep-alive",
  "proxy-authenticate",
  "proxy-authorization",
  "te",
  "trailer",
  "transfer-encoding",
  "upgrade",
]);

export default {
  async fetch(request: Request, env: Env, ctx: ExecutionContext): Promise<Response> {
    if (request.method !== "GET" && request.method !== "HEAD") {
      return fetch(originRequest(request, env));
    }

    if (request.headers.has("Range")) {
      const response = await resolveOriginResponse(request, env);
      return markCache(response, "BYPASS", request.method);
    }

    const cacheKey = edgeCacheKey(request);
    const cached = await caches.default.match(cacheKey);
    if (cached) {
      return markCache(cached, "HIT", request.method);
    }

    const response = await resolveOriginResponse(request, env);
    const output = markCache(response, "MISS", request.method);
    if (request.method === "GET" && shouldStore(output)) {
      ctx.waitUntil(caches.default.put(cacheKey, output.clone()));
    }
    return output;
  },
};

export async function resolveOriginResponse(request: Request, env: Env): Promise<Response> {
  const originResponse = await fetch(originRequest(request, env));
  if (!isStorageRedirect(originResponse)) {
    return normalizeOriginResponse(originResponse, request, env);
  }
  try {
    return await fetchStorageRedirect(request, originResponse, env);
  } catch (error) {
    if (env.EDGE_BYPASS_SECRET) {
      const fallback = await fetch(originRequest(request, env, true));
      if (!isStorageRedirect(fallback)) {
        return withEdgeHeader(normalizeOriginResponse(fallback, request, env), "X-SuperCDN-Edge-Fallback", "origin");
      }
    }
    return new Response(`storage fetch failed: ${errorMessage(error)}`, {
      status: 502,
      headers: {
        "Content-Type": "text/plain; charset=utf-8",
        "Cache-Control": "no-store",
        "X-SuperCDN-Edge-Error": "storage_fetch_failed",
      },
    });
  }
}

export function isStorageRedirect(response: Response): boolean {
  return (
    storageRedirectStatus.has(response.status) &&
    response.headers.get("X-SuperCDN-Redirect")?.toLowerCase() === "storage" &&
    response.headers.has("Location")
  );
}

export async function fetchStorageRedirect(request: Request, response: Response, env: Env): Promise<Response> {
  const location = response.headers.get("Location");
  if (!location) {
    return normalizeOriginResponse(response, request, env);
  }
  const storageResponse = await fetch(location, {
    method: request.method === "HEAD" ? "HEAD" : "GET",
    headers: storageHeaders(request),
    redirect: "manual",
  });
  const followed = await followStorageRedirects(storageResponse, request, 3);
  if (followed.status >= 400 && env.EDGE_BYPASS_SECRET) {
    throw new Error(`storage returned ${followed.status}`);
  }
  return normalizeStorageResponse(followed, request, response, env);
}

async function followStorageRedirects(response: Response, request: Request, remaining: number): Promise<Response> {
  if (remaining <= 0 || !storageRedirectStatus.has(response.status) || !response.headers.has("Location")) {
    return response;
  }
  const location = response.headers.get("Location");
  if (!location) {
    return response;
  }
  return followStorageRedirects(
    await fetch(location, {
      method: request.method === "HEAD" ? "HEAD" : "GET",
      headers: storageHeaders(request),
      redirect: "manual",
    }),
    request,
    remaining - 1,
  );
}

export function originRequest(request: Request, env: Env, forceOrigin = false): Request {
  const source = new URL(request.url);
  const origin = new URL(env.ORIGIN_BASE_URL);
  origin.pathname = source.pathname;
  origin.search = source.search;

  const headers = sanitizeOriginHeaders(request.headers);
  headers.set("X-Forwarded-Host", source.host);
  headers.set("X-Forwarded-Proto", source.protocol.replace(":", ""));
  if (forceOrigin && env.EDGE_BYPASS_SECRET) {
    headers.set("X-SuperCDN-Origin-Delivery", "origin");
    headers.set("X-SuperCDN-Edge-Secret", env.EDGE_BYPASS_SECRET);
  }

  return new Request(origin.toString(), {
    method: request.method,
    headers,
    body: request.body,
    redirect: "manual",
  });
}

export function storageHeaders(request: Request): Headers {
  const out = new Headers();
  copyHeader(request.headers, out, "Accept");
  copyHeader(request.headers, out, "Accept-Language");
  copyHeader(request.headers, out, "Range");
  copyHeader(request.headers, out, "If-None-Match");
  copyHeader(request.headers, out, "If-Modified-Since");
  copyHeader(request.headers, out, "User-Agent");
  return out;
}

function sanitizeOriginHeaders(headers: Headers): Headers {
  const out = new Headers(headers);
  out.delete("Host");
  out.delete("Cf-Connecting-Ip");
  for (const name of hopByHopHeaders) {
    out.delete(name);
  }
  return out;
}

function copyHeader(source: Headers, target: Headers, name: string): void {
  const value = source.get(name);
  if (value) {
    target.set(name, value);
  }
}

function normalizeOriginResponse(response: Response, request: Request, env: Env): Response {
  return normalizeResponse(response, request, undefined, env);
}

function normalizeStorageResponse(response: Response, request: Request, redirectResponse: Response, env: Env): Response {
  const out = normalizeResponse(response, request, redirectResponse.headers, env);
  out.headers.delete("Location");
  out.headers.delete("Set-Cookie");
  out.headers.set("X-SuperCDN-Edge-Source", "storage");
  return out;
}

function normalizeResponse(response: Response, request: Request, fallbackHeaders?: Headers, env?: Env): Response {
  const headers = new Headers(response.headers);
  for (const name of hopByHopHeaders) {
    headers.delete(name);
  }
  headers.delete("X-SuperCDN-Redirect");

  const fallbackCacheControl = fallbackHeaders?.get("Cache-Control");
  if (!headers.get("Cache-Control") && fallbackCacheControl) {
    headers.set("Cache-Control", fallbackCacheControl);
  }
  if (!headers.get("Cache-Control")) {
    headers.set("Cache-Control", defaultCacheControl(request, env));
  }

  const guessed = contentTypeByPath(new URL(request.url).pathname);
  const currentType = headers.get("Content-Type") || "";
  if (guessed && (currentType === "" || currentType.toLowerCase().startsWith("application/octet-stream"))) {
    headers.set("Content-Type", guessed);
  }

  if (request.method === "HEAD") {
    return new Response(null, {
      status: response.status,
      statusText: response.statusText,
      headers,
    });
  }
  return new Response(response.body, {
    status: response.status,
    statusText: response.statusText,
    headers,
  });
}

function markCache(response: Response, value: string, method: string): Response {
  const out = method === "HEAD" ? new Response(null, response) : new Response(response.body, response);
  out.headers.set("X-SuperCDN-Cache", value);
  return out;
}

function withEdgeHeader(response: Response, name: string, value: string): Response {
  const out = new Response(response.body, response);
  out.headers.set(name, value);
  return out;
}

function shouldStore(response: Response): boolean {
  if (!cacheableStatus.has(response.status) || response.status === 206) {
    return false;
  }
  if (response.headers.has("Set-Cookie")) {
    return false;
  }
  const cacheControl = response.headers.get("Cache-Control")?.toLowerCase() || "";
  return !cacheControl.includes("no-store") && !cacheControl.includes("private");
}

function edgeCacheKey(request: Request): Request {
  return new Request(request.url, { method: "GET" });
}

function defaultCacheControl(request: Request, env?: Env): string {
  if (env?.EDGE_DEFAULT_CACHE_CONTROL) {
    return env.EDGE_DEFAULT_CACHE_CONTROL;
  }
  const path = new URL(request.url).pathname;
  if (path === "/" || path.endsWith(".html")) {
    return "public, max-age=60";
  }
  return "public, max-age=300";
}

function contentTypeByPath(pathname: string): string {
  const lower = pathname.toLowerCase();
  if (lower.endsWith(".html") || lower.endsWith(".htm")) return "text/html; charset=utf-8";
  if (lower.endsWith(".js") || lower.endsWith(".mjs")) return "text/javascript; charset=utf-8";
  if (lower.endsWith(".css")) return "text/css; charset=utf-8";
  if (lower.endsWith(".json")) return "application/json";
  if (lower.endsWith(".svg")) return "image/svg+xml";
  if (lower.endsWith(".wasm")) return "application/wasm";
  if (lower.endsWith(".woff")) return "font/woff";
  if (lower.endsWith(".woff2")) return "font/woff2";
  return "";
}

function errorMessage(error: unknown): string {
  if (error instanceof Error) {
    return error.message;
  }
  return String(error);
}
