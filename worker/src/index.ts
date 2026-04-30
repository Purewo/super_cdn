export interface Env {
  ASSETS?: Fetcher;
  ORIGIN_BASE_URL: string;
  EDGE_BYPASS_SECRET?: string;
  EDGE_DEFAULT_CACHE_CONTROL?: string;
  EDGE_MANIFEST?: KVNamespace;
  EDGE_MANIFEST_DRY_RUN?: string;
  EDGE_MANIFEST_JSON?: string;
  EDGE_MANIFEST_KEY?: string;
  EDGE_MANIFEST_KEY_PREFIX?: string;
  EDGE_MANIFEST_MODE?: string;
  EDGE_STATIC_ASSETS?: string;
}

export interface EdgeManifest {
  version: number;
  kind?: string;
  site_id?: string;
  deployment_id?: string;
  deployment_target?: string;
  route_profile?: string;
  mode?: string;
  rules?: SiteRules;
  routes: Record<string, EdgeManifestRoute>;
  fallback?: EdgeManifestRoute;
  not_found?: EdgeManifestRoute;
  warnings?: string[];
}

export interface EdgeManifestRoute {
  type: string;
  delivery?: string;
  file?: string;
  status?: number;
  location?: string;
  content_type?: string;
  cache_control?: string;
  object_cache_control?: string;
  size?: number;
  sha256?: string;
  object_id?: number;
  object_key?: string;
  headers?: Record<string, string>;
}

interface SiteRules {
  mode?: string;
  redirects?: SiteRedirectRule[];
  rewrites?: SiteRewriteRule[];
}

interface SiteRedirectRule {
  from?: string;
  to?: string;
  status?: number;
}

interface SiteRewriteRule {
  from?: string;
  to?: string;
}

export interface EdgeManifestDecision {
  action: "site_redirect" | "route" | "fallback" | "not_found" | "miss";
  request_path: string;
  serve_path: string;
  route_type?: string;
  delivery?: string;
  file?: string;
  status: number;
  location?: string;
  content_type?: string;
  cache_control?: string;
  object_cache_control?: string;
  headers?: Record<string, string>;
  reason?: string;
}

interface LoadedEdgeManifest {
  key: string;
  manifest?: EdgeManifest;
  error?: string;
}

const cacheableStatus = new Set([200, 404]);
const storageRedirectStatus = new Set([301, 302, 303, 307, 308]);
const cachedCacheControlHeader = "X-SuperCDN-Cached-Cache-Control";
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
    if (edgeManifestDryRunRequested(request, env)) {
      return edgeManifestDryRunResponse(request, env);
    }

    if (request.method !== "GET" && request.method !== "HEAD") {
      if (edgeStaticAssetsEnabled(env)) {
        return methodNotAllowedResponse();
      }
      return fetch(originRequest(request, env));
    }

    const manifestResponse = await edgeManifestRouteResponse(request, env);
    if (manifestResponse) {
      return manifestResponse;
    }

    const staticResponse = await edgeStaticAssetsResponse(request, env);
    if (staticResponse) {
      return staticResponse;
    }

    if (request.headers.has("Range")) {
      const response = await resolveOriginResponse(request, env);
      return markCache(response, "BYPASS", request.method);
    }

    const cacheKey = edgeCacheKey(request);
    const cached = await caches.default.match(cacheKey);
    if (cached) {
      return markCache(restoreCachedCacheControl(cached), "HIT", request.method);
    }

    const response = await resolveOriginResponse(request, env);
    const output = markCache(response, "MISS", request.method);
    if (request.method === "GET" && shouldStore(output)) {
      ctx.waitUntil(caches.default.put(cacheKey, responseForCache(output.clone())));
    }
    return output;
  },
};

export function edgeManifestDryRunRequested(request: Request, env: Env): boolean {
  if (!enabled(env.EDGE_MANIFEST_DRY_RUN)) {
    return false;
  }
  const url = new URL(request.url);
  const query = (url.searchParams.get("__supercdn_edge_manifest") || "").toLowerCase();
  const header = (request.headers.get("X-SuperCDN-Edge-Manifest-Dry-Run") || "").toLowerCase();
  return query === "dry-run" || query === "1" || enabled(header);
}

export async function edgeManifestDryRunResponse(request: Request, env: Env): Promise<Response> {
  const loaded = await loadEdgeManifest(request, env);
  if (!loaded.manifest) {
    return edgeManifestJSON(
      {
        ok: false,
        source: "edge_manifest",
        key: loaded.key,
        error: loaded.error || "edge manifest unavailable",
      },
      503,
    );
  }
  const decision = resolveEdgeManifestDecision(request, loaded.manifest);
  return edgeManifestJSON({
    ok: true,
    source: "edge_manifest",
    key: loaded.key,
    site_id: loaded.manifest.site_id,
    deployment_id: loaded.manifest.deployment_id,
    deployment_target: loaded.manifest.deployment_target,
    route_profile: loaded.manifest.route_profile,
    manifest_version: loaded.manifest.version,
    manifest_kind: loaded.manifest.kind,
    manifest_mode: loaded.manifest.mode,
    route_count: Object.keys(loaded.manifest.routes || {}).length,
    decision,
    warnings: loaded.manifest.warnings || [],
  });
}

export async function edgeManifestRouteResponse(request: Request, env: Env): Promise<Response | undefined> {
  if (!edgeManifestRoutingEnabled(env)) {
    return undefined;
  }
  const loaded = await loadEdgeManifest(request, env);
  if (!loaded.manifest) {
    if (edgeManifestRoutingStrict(env) && !edgeStaticAssetsEnabled(env)) {
      return edgeManifestRouteErrorResponse(503, "manifest_unavailable", loaded.error || "edge manifest unavailable");
    }
    return undefined;
  }
  const decision = resolveEdgeManifestDecision(request, loaded.manifest);
  const response = directEdgeManifestResponse(decision);
  if (response) {
    return response;
  }
  if (edgeManifestRoutingStrict(env) && !edgeStaticAssetsEnabled(env)) {
    return unresolvedEdgeManifestDecisionResponse(decision);
  }
  return undefined;
}

export async function edgeStaticAssetsResponse(request: Request, env: Env): Promise<Response | undefined> {
  if (!edgeStaticAssetsEnabled(env)) {
    return undefined;
  }
  if (!env.ASSETS) {
    return edgeStaticAssetsErrorResponse(503, "cloudflare_static_binding_missing", "ASSETS binding is not configured");
  }
  try {
    const response = await env.ASSETS.fetch(request);
    return withEdgeHeader(normalizeResponse(response, request, undefined, env), "X-SuperCDN-Edge-Source", "cloudflare_static");
  } catch (error) {
    return edgeStaticAssetsErrorResponse(
      502,
      "cloudflare_static_fetch_failed",
      `Cloudflare Static Assets fetch failed: ${errorMessage(error)}`,
    );
  }
}

export async function loadEdgeManifest(request: Request, env: Env): Promise<LoadedEdgeManifest> {
  const key = edgeManifestKey(request, env);
  if (env.EDGE_MANIFEST_JSON?.trim()) {
    return parseEdgeManifest(env.EDGE_MANIFEST_JSON, key);
  }
  if (!env.EDGE_MANIFEST) {
    return { key, error: "EDGE_MANIFEST KV binding is not configured" };
  }
  const raw = await env.EDGE_MANIFEST.get(key);
  if (!raw) {
    return { key, error: "edge manifest not found" };
  }
  return parseEdgeManifest(raw, key);
}

export function edgeManifestKey(request: Request, env: Env): string {
  const explicit = env.EDGE_MANIFEST_KEY?.trim();
  if (explicit) {
    return explicit;
  }
  const host = new URL(request.url).hostname.toLowerCase();
  const prefix = env.EDGE_MANIFEST_KEY_PREFIX ?? "sites/";
  return `${prefix}${host}/active/edge-manifest`;
}

export function resolveEdgeManifestDecision(request: Request, manifest: EdgeManifest): EdgeManifestDecision {
  const requestPath = cleanEdgePath(new URL(request.url).pathname);
  const rules = manifest.rules || {};
  for (const rule of rules.redirects || []) {
    if (siteRuleMatch(rule.from || "", requestPath)) {
      return {
        action: "site_redirect",
        request_path: requestPath,
        serve_path: requestPath,
        status: rule.status || 302,
        location: rule.to || "/",
        reason: "matched_redirect_rule",
      };
    }
  }

  let servePath = requestPath;
  for (const rule of rules.rewrites || []) {
    if (rule.to && siteRuleMatch(rule.from || "", requestPath)) {
      servePath = cleanEdgePath(rule.to);
      break;
    }
  }

  const route = manifest.routes?.[servePath];
  if (route) {
    return decisionFromRoute("route", requestPath, servePath, route, "matched_route");
  }
  if (manifest.fallback) {
    return decisionFromRoute("fallback", requestPath, servePath, manifest.fallback, "spa_fallback");
  }
  if (manifest.not_found) {
    return decisionFromRoute("not_found", requestPath, servePath, manifest.not_found, "not_found");
  }
  return {
    action: "miss",
    request_path: requestPath,
    serve_path: servePath,
    status: 404,
    reason: "manifest_route_not_found",
  };
}

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

function responseForCache(response: Response): Response {
  const out = new Response(response.body, response);
  const cacheControl = response.headers.get("Cache-Control");
  if (cacheControl) {
    out.headers.set(cachedCacheControlHeader, cacheControl);
  }
  return out;
}

function restoreCachedCacheControl(response: Response): Response {
  const cacheControl = response.headers.get(cachedCacheControlHeader);
  if (!cacheControl) {
    return response;
  }
  const out = new Response(response.body, response);
  out.headers.set("Cache-Control", cacheControl);
  out.headers.delete(cachedCacheControlHeader);
  return out;
}

function withEdgeHeader(response: Response, name: string, value: string): Response {
  const out = new Response(response.body, response);
  out.headers.set(name, value);
  return out;
}

function directEdgeManifestResponse(decision: EdgeManifestDecision): Response | undefined {
  if (decision.action === "site_redirect" && decision.location) {
    return edgeManifestRedirectResponse(decision);
  }
  if (decision.route_type === "redirect" && decision.location) {
    return edgeManifestRedirectResponse(decision);
  }
  return undefined;
}

function edgeManifestRedirectResponse(decision: EdgeManifestDecision): Response {
  const headers = new Headers();
  headers.set("Location", decision.location || "/");
  applyManifestHeaders(headers, decision);

  if (!headers.has("Cache-Control") && decision.route_type === "redirect") {
    headers.set("Cache-Control", "no-store");
  }
  headers.set("X-SuperCDN-Edge-Manifest", "route");
  headers.set("X-SuperCDN-Edge-Action", decision.action);
  headers.set("X-SuperCDN-Edge-Source", "manifest");
  if (decision.file) {
    headers.set("X-SuperCDN-Edge-File", decision.file);
  }

  return new Response(null, {
    status: redirectStatus(decision.status),
    headers,
  });
}

function unresolvedEdgeManifestDecisionResponse(decision: EdgeManifestDecision): Response {
  const status = decision.action === "miss" ? 404 : decision.status >= 400 ? decision.status : 502;
  const reason = decision.action === "miss" ? "manifest_route_not_found" : "manifest_direct_route_unavailable";
  return edgeManifestRouteErrorResponse(
    status,
    reason,
    `edge manifest cannot serve ${decision.request_path} without origin fallback`,
  );
}

function edgeManifestRouteErrorResponse(status: number, reason: string, message: string): Response {
  return new Response(`${message}\n`, {
    status,
    headers: {
      "Content-Type": "text/plain; charset=utf-8",
      "Cache-Control": "no-store",
      "X-SuperCDN-Edge-Manifest": "route",
      "X-SuperCDN-Edge-Error": reason,
    },
  });
}

function edgeStaticAssetsErrorResponse(status: number, reason: string, message: string): Response {
  return new Response(`${message}\n`, {
    status,
    headers: {
      "Content-Type": "text/plain; charset=utf-8",
      "Cache-Control": "no-store",
      "X-SuperCDN-Edge-Source": "cloudflare_static",
      "X-SuperCDN-Edge-Error": reason,
    },
  });
}

function applyManifestHeaders(headers: Headers, decision: EdgeManifestDecision): void {
  for (const [name, value] of Object.entries(decision.headers || {})) {
    setManifestHeader(headers, name, value);
  }
  if (decision.cache_control) {
    headers.set("Cache-Control", decision.cache_control);
  }
}

function setManifestHeader(headers: Headers, name: string, value: string): void {
  const normalized = name.trim().toLowerCase();
  if (
    normalized === "" ||
    normalized === "location" ||
    normalized === "set-cookie" ||
    normalized === "content-length" ||
    normalized === "content-encoding" ||
    hopByHopHeaders.has(normalized)
  ) {
    return;
  }
  try {
    headers.set(name, value);
  } catch {
    // Ignore invalid custom header names or values from a manifest.
  }
}

function redirectStatus(status: number): number {
  return storageRedirectStatus.has(status) ? status : 302;
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

function parseEdgeManifest(raw: string, key: string): LoadedEdgeManifest {
  try {
    const manifest = JSON.parse(raw) as EdgeManifest;
    if (!manifest || typeof manifest !== "object" || !manifest.routes || typeof manifest.routes !== "object") {
      return { key, error: "edge manifest is missing routes" };
    }
    return { key, manifest };
  } catch (error) {
    return { key, error: `invalid edge manifest json: ${errorMessage(error)}` };
  }
}

function decisionFromRoute(
  action: EdgeManifestDecision["action"],
  requestPath: string,
  servePath: string,
  route: EdgeManifestRoute,
  reason: string,
): EdgeManifestDecision {
  return {
    action,
    request_path: requestPath,
    serve_path: servePath,
    route_type: route.type,
    delivery: route.delivery,
    file: route.file,
    status: route.status || (route.type === "redirect" ? 302 : 200),
    location: route.location,
    content_type: route.content_type,
    cache_control: route.cache_control,
    object_cache_control: route.object_cache_control,
    headers: route.headers,
    reason,
  };
}

function cleanEdgePath(value: string): string {
  const raw = `/${value.replace(/\\/g, "/").replace(/^\/+/, "")}`;
  const trailingSlash = raw.length > 1 && raw.endsWith("/");
  const parts: string[] = [];
  for (const part of raw.split("/")) {
    if (part === "" || part === ".") {
      continue;
    }
    if (part === "..") {
      parts.pop();
      continue;
    }
    parts.push(part);
  }
  const cleaned = `/${parts.join("/")}`;
  return trailingSlash && cleaned !== "/" ? `${cleaned}/` : cleaned;
}

function cleanSiteRulePath(value: string): string {
  let rule = value.trim().replace(/\\/g, "/");
  if (rule === "" || rule === "*") {
    return "/*";
  }
  if (!rule.startsWith("/")) {
    rule = `/${rule}`;
  }
  if (rule.endsWith("*")) {
    const prefix = rule.slice(0, -1);
    const cleaned = cleanEdgePath(prefix).replace(/\/+$/, "") || "/";
    if (prefix.endsWith("/") && cleaned !== "/") {
      return `${cleaned}/*`;
    }
    return `${cleaned}*`;
  }
  return cleanEdgePath(rule);
}

function siteRuleMatch(pattern: string, requestPath: string): boolean {
  const rule = cleanSiteRulePath(pattern);
  const path = cleanEdgePath(requestPath);
  if (rule === "/*") {
    return true;
  }
  if (rule.endsWith("*")) {
    return path.startsWith(rule.slice(0, -1));
  }
  return rule === path;
}

function edgeManifestJSON(body: unknown, status = 200): Response {
  return new Response(`${JSON.stringify(body, null, 2)}\n`, {
    status,
    headers: {
      "Content-Type": "application/json; charset=utf-8",
      "Cache-Control": "no-store",
      "X-SuperCDN-Edge-Manifest-Dry-Run": "true",
    },
  });
}

function enabled(value: string | undefined): boolean {
  const normalized = (value || "").trim().toLowerCase();
  return normalized === "1" || normalized === "true" || normalized === "yes" || normalized === "on";
}

function edgeManifestRoutingEnabled(env: Env): boolean {
  const mode = edgeManifestRoutingMode(env);
  return mode === "route" || mode === "active" || mode === "strict" || mode === "enforce";
}

function edgeManifestRoutingStrict(env: Env): boolean {
  const mode = edgeManifestRoutingMode(env);
  return mode === "strict" || mode === "enforce";
}

function edgeManifestRoutingMode(env: Env): string {
  return (env.EDGE_MANIFEST_MODE || "").trim().toLowerCase();
}

function edgeStaticAssetsEnabled(env: Env): boolean {
  return enabled(env.EDGE_STATIC_ASSETS);
}

function methodNotAllowedResponse(): Response {
  return new Response("method not allowed\n", {
    status: 405,
    headers: {
      Allow: "GET, HEAD",
      "Content-Type": "text/plain; charset=utf-8",
      "Cache-Control": "no-store",
      "X-SuperCDN-Edge-Source": "cloudflare_static",
    },
  });
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
