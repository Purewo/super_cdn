# Super CDN CLI Reference

本文档按“命令 -> HTTP API -> 参数 -> 返回”的方式记录 `supercdnctl`，用于快速检索和交接。

## 全局约定

所有 `supercdnctl` 命令都调用 Super CDN HTTP API。管理 API 需要 Bearer Token：

```powershell
$env:SUPERCDN_TOKEN = "change-me"
.\bin\supercdnctl.exe -server http://127.0.0.1:8080 <command> ...
```

全局参数：

| 参数 | 环境变量 | 默认值 | 说明 |
| --- | --- | --- | --- |
| `-server` | `SUPERCDN_URL` | `http://127.0.0.1:8080` | Super CDN 服务地址 |
| `-token` | `SUPERCDN_TOKEN` | 空 | 管理 Token，必填 |

返回值：

- 成功时输出 JSON，和 HTTP API 返回体保持一致。
- 失败时 CLI 退出码非 0，并输出 `error: ...`。
- `upload` 会先调用 preflight；`deploy-site` 由服务端解压产物包后执行站点级检查，并按原始目录结构上传站点文件。

代理约定：

- CLI 不控制 R2、IPFS、AList 代理。代理只由服务端配置里的 `proxy_url` 决定。
- `proxy_url` 为空时服务不会读取系统环境变量 `HTTP_PROXY`、`HTTPS_PROXY`、`NO_PROXY`。

## 命令索引

| 命令 | HTTP API | 用途 |
| --- | --- | --- |
| `create-project` | `POST /api/v1/projects` | 创建普通静态资源项目 |
| `upload` | `POST /api/v1/preflight/upload` + `POST /api/v1/assets` | 上传普通静态资源 |
| `create-site` | `POST /api/v1/sites` | 创建静态站点 |
| `deploy-site` | `GET /api/v1/sites/{id}/deployment-target` + deploy API | 部署静态站点产物包或目录 |
| `probe-site` | 本地 HTTP 探测 + 可选部署查询 | 验证线上 HTML、重定向 JS/CSS 的 MIME/CORS，以及可选 SPA fallback |
| `list-deployments` | `GET /api/v1/sites/{id}/deployments` | 列出站点部署历史 |
| `deployment` | `GET /api/v1/sites/{id}/deployments/{deployment}` | 查询单个部署 |
| `export-edge-manifest` | `GET /api/v1/sites/{id}/deployments/{deployment}/edge-manifest` | 导出可用于边缘路由的部署 manifest |
| `publish-edge-manifest` | `POST /api/v1/sites/{id}/deployments/{deployment}/edge-manifest/publish` | 发布边缘 manifest 到 Cloudflare Workers KV |
| `refresh-edge-manifest` | `POST /api/v1/sites/{id}/deployments/{deployment}/edge-manifest/publish` | 刷新 active edge manifest 并默认执行 hybrid 探测 |
| `publish-cloudflare-static` | 本地 Wrangler 调用 | 发布本地目录到 Cloudflare Workers Static Assets |
| `promote-deployment` | `POST /api/v1/sites/{id}/deployments/{deployment}/promote` | 将部署提升为当前生产版本 |
| `delete-deployment` | `DELETE /api/v1/sites/{id}/deployments/{deployment}` | 删除未激活且未 pinned 的部署 |
| `gc-site` | `POST /api/v1/sites/{id}/gc` | 站点内容清理入口 |
| `init-libraries` | `POST /api/v1/init/resource-libraries` | 初始化资源库目录结构 |
| `init-job` | `GET /api/v1/init/jobs/{id}` | 查询初始化任务 |
| `resource-status` | `GET /api/v1/resource-libraries/status` | 查询资源库状态和本地健康缓存 |
| `health-check` | `POST /api/v1/resource-libraries/health-check` | 执行资源库健康检查 |
| `e2e-probe` | `POST /api/v1/resource-libraries/e2e-probe` | 执行真实上传/读取/清理探针 |
| `create-bucket` | `POST /api/v1/asset-buckets` | 创建静态资源桶 |
| `create-cdn-bucket` | `POST /api/v1/asset-buckets` | 创建海外 CDN 资源桶快捷命令 |
| `create-domestic-cdn-bucket` | `POST /api/v1/asset-buckets` | 创建国内 AList/OpenList CDN 资源桶快捷命令 |
| `create-mobile-cdn-bucket` | `POST /api/v1/asset-buckets` | 创建移动线路 CDN 资源桶快捷命令 |
| `init-bucket` | `POST /api/v1/asset-buckets/{slug}/init` | 初始化桶目录结构 |
| `upload-bucket` | `POST /api/v1/asset-buckets/{slug}/objects` | 上传桶对象 |
| `list-bucket` | `GET /api/v1/asset-buckets/{slug}/objects` | 列出桶对象 |
| `purge-bucket` | `POST /api/v1/asset-buckets/{slug}/purge` | 按桶对象 URL 清 Cloudflare 缓存 |
| `warmup-bucket` | `POST /api/v1/asset-buckets/{slug}/warmup` | 按桶对象 URL 预热或探测公开访问 |
| `delete-bucket-object` | `DELETE /api/v1/asset-buckets/{slug}/objects` | 删除桶内单个对象 |
| `delete-bucket` | `DELETE /api/v1/asset-buckets/{slug}` | 删除整个桶 |
| `job` | `GET /api/v1/jobs/{id}` | 查询异步任务 |
| `replicas` | `GET /api/v1/objects/{id}/replicas` | 查询对象副本 |
| `purge` | `POST /api/v1/cache/purge` | 调 Cloudflare 清缓存 |
| `sync-site-dns` | `POST /api/v1/sites/{id}/dns` | 同步站点 DNS 记录 |
| `sync-worker-routes` | `POST /api/v1/sites/{id}/worker-routes` | 同步站点 Worker routes |
| `sync-cloudflare-r2` | `POST /api/v1/cloudflare/r2/sync` | 同步 Cloudflare R2 CORS 和公开域名 |
| `provision-cloudflare-r2` | `POST /api/v1/cloudflare/r2/provision` | 创建 Cloudflare R2 bucket 并同步 CORS/公开域名 |
| `create-r2-credentials` | `POST /api/v1/cloudflare/r2/credentials` | 创建 R2 S3 凭证并可写回本地配置 |
| `set-r2-credentials` | 本地配置写入 | 导入已有 R2 S3 凭证 |
| `purge-site` | `POST /api/v1/sites/{id}/purge` | 按站点或部署 manifest 清 Cloudflare 缓存 |

## 普通静态资源

### create-project

创建普通资源项目。公开访问路径为 `/o/{project}/{path}`。

```powershell
.\bin\supercdnctl.exe create-project -id assets
```

HTTP:

```http
POST /api/v1/projects
Authorization: Bearer <token>
Content-Type: application/json
```

请求体：

```json
{
  "id": "assets"
}
```

参数：

| 参数 | 必填 | 说明 |
| --- | --- | --- |
| `-id` | 是 | 项目 ID，只允许安全 ID 字符 |

### upload

上传普通静态资源。CLI 会先执行 preflight，再上传文件。

```powershell
.\bin\supercdnctl.exe upload -project assets -file .\README.md -path docs/readme.txt -profile china_all
```

HTTP:

```http
POST /api/v1/preflight/upload
POST /api/v1/assets
```

multipart 字段：

| 字段 | 来源参数 | 必填 | 说明 |
| --- | --- | --- | --- |
| `file` | `-file` | 是 | 本地文件 |
| `project_id` | `-project` | 是 | 项目 ID |
| `path` | `-path` | 是 | 项目内逻辑路径 |
| `route_profile` | `-profile` | 否 | 默认 `overseas` |
| `cache_control` | `-cache-control` | 否 | 覆盖缓存策略 |

返回核心字段：

| 字段 | 说明 |
| --- | --- |
| `object` | 本地对象记录 |
| `jobs` | 备份副本异步任务 |
| `url` | 公开 URL |

## 静态站点

### create-site

创建站点和域名绑定。

```powershell
.\bin\supercdnctl.exe create-site -site blog -profile china_all -mode spa -domains example.com,www.example.com
```

`create-site` also accepts `-target origin_assisted|cloudflare_static|hybrid_edge`. When omitted, the route profile default is used.

HTTP:

```http
POST /api/v1/sites
```

参数：

| 参数 | 必填 | 默认值 | 说明 |
| --- | --- | --- | --- |
| `-site` | 是 | 空 | 站点 ID |
| `-name` | 否 | 空 | 站点显示名，方便人工记忆 |
| `-profile` | 否 | `overseas` | 路由配置 |
| `-mode` | 否 | `standard` | `standard` 或 `spa` |
| `-domains` | 否 | 空 | 逗号分隔域名 |

### deploy-site

部署静态站点。CLI 可以上传已有 zip 产物包，也可以把本地目录临时打包后上传。服务端会本地解压、生成 manifest，并按原始目录结构上传站点文件；远端资源库不需要支持在线解压。

```powershell
.\bin\supercdnctl.exe deploy-site -site blog -dir .\dist -profile china_all
.\bin\supercdnctl.exe deploy-site -site blog -bundle .\dist.zip -env preview
.\bin\supercdnctl.exe deploy-site -site blog -bundle .\dist.zip -env production -promote
.\bin\supercdnctl.exe deploy-site -site blog -dir .\dist -target cloudflare_static -domains blog-static.example.com -static-name supercdn-blog-static
.\bin\supercdnctl.exe deploy-site -site blog -dir .\dist -profile overseas
```

`deploy-site` also accepts `-target origin_assisted|cloudflare_static|hybrid_edge`. `deployment_target` is the website hosting target, not the storage route profile. For ordinary overseas static sites use `cloudflare_static`; keep R2 for large objects such as video, images and archives; use `hybrid_edge` when Worker/KV routing should split resources between Cloudflare and AList/OpenList.

When `-target` is omitted, `deploy-site` calls `GET /api/v1/sites/{id}/deployment-target` to resolve the existing site target or the route-profile default. If the resolved target is `cloudflare_static` and `-domains` is empty, the server returns existing site domains or a one-level subdomain under `cloudflare.root_domain`, and the CLI passes them to Wrangler automatically. This keeps Cloudflare Static custom domains on hosts like `blog.qwk.ccwu.cc`; the nested `cloudflare.site_domain_suffix` pattern is kept for Go-origin default site domains.

When `-target cloudflare_static` is used, the CLI publishes `-dir` with local Wrangler Workers Static Assets, then records the resulting Worker/domain/version metadata through the Super CDN API. This path does not upload a zip artifact to R2 or the Go-origin storage chain.

Cloudflare Static deploys default to `-static-cache-policy auto`. If the source already contains `_headers`, the CLI uses it. Otherwise it publishes from a temporary copy with a generated `_headers` file. The generated policy keeps `/`, HTML and service-worker files revalidating, gives versioned or common build assets long immutable browser caching, and gives other files a short revalidating cache. The source directory is not modified.

For SPAs, pass `-static-spa`. The CLI generates a temporary `wrangler.toml` with `assets.not_found_handling = "single-page-application"` so deep links return `index.html` from Cloudflare Static Assets. `-static-not-found-handling` can also be set directly to `none`、`404-page` or `single-page-application`.

Cloudflare Static deployments run `-static-verify wait` by default. After Wrangler publishes, the CLI probes every custom domain over HTTPS before recording the deployment in Super CDN. It checks root HTML, JS/CSS MIME types, direct same-site asset delivery, generated cache headers, and SPA fallback when enabled. The readiness probe uses `-static-verify-resolver 1.1.1.1:53` by default to avoid stale local DNS wildcard cache. A failed wait-mode probe prints the probe report and returns an error, so the control plane does not mark an unreachable custom domain as active. Use `-static-verify warn` to continue after a failed probe or `-static-verify none` to skip it.

本地检查产物但不上传：

```powershell
.\bin\supercdnctl.exe inspect-site -dir .\dist
.\bin\supercdnctl.exe inspect-site -bundle .\dist.zip
```

HTTP:

```http
GET /api/v1/sites/{id}/deployment-target
POST /api/v1/sites/{id}/deployments
POST /api/v1/sites/{id}/cloudflare-static/deployments
```

参数：

| 参数 | 必填 | 默认值 | 说明 |
| --- | --- | --- | --- |
| `-site` | 是 | 空 | 站点 ID |
| `-dir` | 否 | 空 | 本地构建目录，CLI 会临时打 zip |
| `-bundle` | 否 | 空 | 已有 zip 产物包；`-dir` 和 `-bundle` 二选一 |
| `-env` | 否 | `production` | `production` 或 `preview` |
| `-promote` | 否 | production 默认 true | 部署完成后提升为生产版本 |
| `-pinned` | 否 | `false` | 防止自动保留策略清理 |
| `-wait` | 否 | `true` | 等待异步部署完成 |
| `-timeout` | 否 | `30m` | 等待超时时间 |
| `-profile` | 否 | 站点默认线路 | 覆盖站点默认路由 |
| `-target` | 否 | 线路默认值 | `origin_assisted`、`cloudflare_static` 或 `hybrid_edge` |
| `-domains` | 否 | 空 | `cloudflare_static` 自定义域名，逗号分隔 |
| `-static-name` | 否 | `supercdn-{site}-static` | `cloudflare_static` Worker 名称 |
| `-compatibility-date` | 否 | 当前 UTC 日期 | `cloudflare_static` Worker compatibility date |
| `-message` | 否 | 空 | `cloudflare_static` Cloudflare 部署说明 |
| `-static-cache-policy` | 否 | `auto` | `cloudflare_static` 缓存头策略：`auto`、`force` 或 `none` |
| `-static-spa` | 否 | `false` | 为 `cloudflare_static` 启用 SPA fallback |
| `-static-not-found-handling` | 否 | `none` | Cloudflare Static `not_found_handling`：`none`、`404-page` 或 `single-page-application` |
| `-static-verify` | 否 | `wait` | 发布后的域名可用性验证：`wait`、`warn` 或 `none` |
| `-static-verify-timeout` | 否 | `2m` | `wait` 模式最多等待多久 |
| `-static-verify-interval` | 否 | `5s` | `wait` 模式两次探测间隔 |
| `-static-verify-spa-path` | 否 | 自动 | SPA fallback 探测路径；`-static-spa` 时默认 `/__supercdn_spa_probe` |
| `-static-verify-resolver` | 否 | `1.1.1.1:53` | readiness HTTP 探测使用的 DNS 解析器 |
| `-edge-name` | 否 | `supercdn-{site}-edge` | `hybrid_edge` Worker 名称 |
| `-edge-kv-namespace` | 否 | `supercdn-edge-manifest` | `hybrid_edge` edge manifest KV namespace 标题 |
| `-edge-kv-namespace-id` | 否 | 空 | `hybrid_edge` edge manifest KV namespace ID；传入后无需按标题解析 |
| `-edge-manifest-mode` | 否 | `route` | `hybrid_edge` Worker manifest 模式：`route` 或 `enforce` |
| `-edge-default-cache-control` | 否 | `public, max-age=300` | `hybrid_edge` Worker 默认 Cache-Control |

限制：

- preview 默认最多 300 个文件，production 默认最多 1000 个文件。
- 上传 zip 会作为 artifact 保存到资源库；站点文件按原始目录结构写入 `sites/{site}/deployments/{deployment}/root/...`。
- `-target` 省略时由控制面解析：已有站点优先使用站点配置，其次使用 route profile 的 `deployment_target`，最后回退为 `origin_assisted`。
- `cloudflare_static` 的自动默认域名使用 `cloudflare.root_domain` 下的一层子域，避免嵌套默认域名在 Cloudflare Static 自定义域 TLS 上失败。
- `cloudflare_static` 需要 `-dir`，不支持 `-bundle`；它发布到 Cloudflare Workers Static Assets 并写入 Super CDN deployment 记录，不经过 R2。
- `cloudflare_static` 的 `auto` 缓存策略会生成临时 `_headers`，不会修改源目录；如果源目录已有 `_headers`，默认尊重已有文件。
- `cloudflare_static` 的 SPA fallback 通过临时 Wrangler 配置文件实现，不会写入源目录。
- `cloudflare_static` 默认发布后会先验证 HTTPS 域名可访问；验证失败时不会写入 active deployment，除非使用 `-static-verify warn` 或 `none`。
- 公共访问使用当前 active deployment；preview 访问自动加 `X-Robots-Tag: noindex`。
- 站点公开访问时，根目录 `index.html` 由 Go origin 直出；其他成功命中的站点文件（非 Range、非 404）会 `302` 到可用的存储直链。普通 `/o/...` 资产仍按 route profile 的 `allow_redirect` 策略处理。
- deployment 响应包含 `inspect`，只提示 module script、dynamic import、CSS 相对资源、字体、wasm、service worker 等风险，不阻止部署。
- deployment 响应包含 `delivery_summary`，用于确认当前产物中 origin/redirect 文件数量。
- `limits.overclock_mode=true` 会跳过上传大小、文件数、资源库容量/单文件/批量/日上传、健康检查、桶容量/单文件/类型和传输槽限制。响应会返回 `overclock_mode: true` 和风险警告；这可能导致不可预料甚至灾难性的结果。

公开访问：

```text
/s/{site}/
/p/{site}/{deployment}/
```

绑定正式域名后也可按 Host 访问。

### probe-site

对已经上线的站点做 HTTP 级交付探测。`-site` 会通过管理 API 找到当前 active production deployment 的公开 URL；`-url` 可直接探测任意公开地址且不需要 token。指定 `-deployment` 时默认探测该 deployment 的 preview URL，传 `-production` 可改为生产 URL。

```powershell
.\bin\supercdnctl.exe probe-site -site blog -spa-path /movie/123
.\bin\supercdnctl.exe probe-site -site blog -deployment dpl-abc
.\bin\supercdnctl.exe probe-site -url https://blog.sites.example.com/ -max-assets 20
.\bin\supercdnctl.exe probe-site -url https://blog.qwk.ccwu.cc/ -resolver 1.1.1.1:53 -require-direct-assets -require-html-revalidate -require-immutable-assets
.\bin\supercdnctl.exe probe-site -url https://blog.qwk.ccwu.cc/ -spa-path /movie/123 -require-edge-static-html -require-edge-manifest-assets
```

检查内容：

- 首页必须返回 `2xx` 且 `Content-Type` 为 `text/html`。
- 从首页提取同站 JS/CSS 引用，逐个跟随 `302` 到最终地址。
- 对 JS/CSS 检查最终 `Content-Type`，避免缺失资源被 SPA fallback 返回 HTML 后产生浏览器 MIME 错误。
- 如果最终地址跨源，使用 `Origin` 头请求并要求最终响应返回 `Access-Control-Allow-Origin: *` 或匹配的 origin。
- 传 `-spa-path` 时额外请求该路径，并要求返回 HTML。
- 传 `-require-edge-static-html` 时要求 root/SPA 响应带 `X-SuperCDN-Edge-Source: cloudflare_static`。
- 传 `-require-edge-manifest-assets` 时要求 JS/CSS 首跳带 `X-SuperCDN-Edge-Source: manifest` 和 `X-SuperCDN-Edge-Manifest: route`。
- 传 `-resolver 1.1.1.1:53` 时，HTTP 探测会绕开本机 DNS 缓存，用指定解析器确认实际公网接管状态。

### deployments

管理部署历史、预览、提升和回滚。

```powershell
.\bin\supercdnctl.exe list-deployments -site blog
.\bin\supercdnctl.exe deployment -site blog -deployment dpl-abc
.\bin\supercdnctl.exe export-edge-manifest -site blog -deployment dpl-abc -out .\edge-manifest.json
.\bin\supercdnctl.exe publish-edge-manifest -site blog -deployment dpl-abc -kv-namespace supercdn-edge-manifest -dry-run
.\bin\supercdnctl.exe refresh-edge-manifest -site blog -kv-namespace supercdn-edge-manifest -spa-path /movie/123
.\bin\supercdnctl.exe deploy-site -site blog -dir .\dist -target cloudflare_static -domains blog-static-test.example.com
.\bin\supercdnctl.exe deploy-site -site blog -dir .\dist -target hybrid_edge -profile china_mobile -domains blog.qwk.ccwu.cc -static-spa
.\bin\supercdnctl.exe publish-cloudflare-static -site blog -dir .\dist -domains blog-static-test.example.com -dry-run=false
.\bin\supercdnctl.exe promote-deployment -site blog -deployment dpl-abc
.\bin\supercdnctl.exe delete-deployment -site blog -deployment dpl-abc
.\bin\supercdnctl.exe gc-site -site blog
```

注意：`cloudflare_static` 部署不会允许普通 `promote-deployment` 做元数据级回滚，因为 Cloudflare Worker 的真实资产版本不会因此自动切换。要回滚 Cloudflare Static，请重新发布目标产物，或后续使用专门的 Worker rollback 流程。`delete-deployment` 删除 `cloudflare_static` 时只删除 Super CDN 元数据，会在响应里提示不会删除 Worker versions/custom domains。

说明：

- rollback 等价于 `promote-deployment` 一个旧 deployment；preview deployment 也可以被提升为 production。
- active production deployment 不能删除。
- pinned deployment 不能删除。
- `export-edge-manifest` 是只读旁路导出，用于后续 Cloudflare Worker/KV/Pages 边缘路由改造；不会改变当前线上 Go-origin + storage 302 交付链路。
- `publish-edge-manifest` 默认 dry-run；真实写入 KV 需要 `-dry-run=false`，默认会写 deployment key，并且仅当该 deployment 当前 active 时写 `sites/{host}/active/edge-manifest`。
- `deploy-site -target cloudflare_static` 是正式的 Cloudflare-native 静态托管入口：它本地调用 Wrangler 发布 Workers Static Assets，然后把部署记录写回 Super CDN。
- `deploy-site -target hybrid_edge` 会执行完整 no-Go 网站流程：上传线路 deployment、发布 active edge manifest 到 Workers KV、部署带 `ASSETS` 和 `run_worker_first` 的 Worker，然后验证 HTML/SPA 和被 manifest 重定向的资源。
- `hybrid_edge` readiness 会额外检查 root/SPA 的 `X-SuperCDN-Edge-Source: cloudflare_static`，以及 JS/CSS 首跳的 `X-SuperCDN-Edge-Manifest: route`，用来确认请求没有回到 Go origin。
- `publish-cloudflare-static` 是更底层的 Cloudflare canary/诊断入口，只发布本地目录，不写 Super CDN deployment 记录；默认 dry-run，真实发布需要 `-dry-run=false`。它读取 `configs/private/cloudflare.env` 中的 `CF_API_TOKEN` / `CF_ACCOUNT_ID`，不会读取或打印密钥值。
- `gc-site` 当前是保守入口，不做破坏性清理；后续可按 manifest 引用计数清理未引用对象。

`refresh-edge-manifest` defaults to the active production deployment, rewrites the active/deployment KV manifest keys, and then runs the hybrid edge probe. It is the quick recovery path when AList/OpenList route signatures become stale; embedded probe URLs redact signed query values by default.

### supercdn.site.json

站点包根目录可放 `supercdn.site.json`：

```json
{
  "mode": "spa",
  "headers": [
    {"path": "/assets/*", "headers": {"Cache-Control": "public, max-age=31536000, immutable"}}
  ],
  "delivery": [
    {"path": "/assets/*", "mode": "redirect"},
    {"path": "/sw.js", "mode": "origin"}
  ],
  "redirects": [
    {"from": "/old", "to": "/new", "status": 301}
  ],
  "rewrites": [
    {"from": "/docs/*", "to": "/index.html"}
  ],
  "not_found": "404.html"
}
```

`delivery.mode` 支持 `origin`、`redirect`、`auto`。当前默认策略是根目录 `index.html` 走 `origin`，其他成功命中的文件走 `redirect`；`delivery` 主要用于把 JS、CSS、worker、wasm 等风险文件临时拉回 origin。路径规则沿用当前站点规则匹配：精确路径或结尾 `*` 的前缀匹配。

## 资源库运维

### init-libraries

初始化一个或多个资源库绑定路径。会创建默认目录结构并写入 `_supercdn/init.json` 标记。

```powershell
.\bin\supercdnctl.exe init-libraries -libraries repo_china_all -dry-run
.\bin\supercdnctl.exe init-libraries -libraries repo_china_all
```

HTTP:

```http
POST /api/v1/init/resource-libraries
```

参数：

| 参数 | 必填 | 说明 |
| --- | --- | --- |
| `-libraries` | 否 | 逗号分隔资源库名；空表示全部 |
| `-dirs` | 否 | 逗号分隔自定义目录；空表示默认结构 |
| `-dry-run` | 否 | 只返回计划，不创建目录 |

默认目录：

```text
_supercdn/manifests
_supercdn/locks
_supercdn/jobs
assets/buckets
assets/objects
assets/manifests
assets/tmp
sites/bundles
sites/artifacts
sites/deployments
sites/releases
sites/manifests
sites/tmp
```

### init-job

查询资源库初始化任务。

```powershell
.\bin\supercdnctl.exe init-job -id 1
```

HTTP:

```http
GET /api/v1/init/jobs/{id}
```

### resource-status

读取本地资源库状态和健康缓存，不主动写云盘。

```powershell
.\bin\supercdnctl.exe resource-status
.\bin\supercdnctl.exe resource-status -library repo_china_all
```

HTTP:

```http
GET /api/v1/resource-libraries/status?library=repo_china_all
```

### health-check

执行资源库健康检查。默认是被动检查，只 list 绑定根目录。

```powershell
.\bin\supercdnctl.exe health-check -libraries repo_china_all
.\bin\supercdnctl.exe health-check -libraries repo_china_all -force
.\bin\supercdnctl.exe health-check -libraries repo_china_all -write-probe
```

HTTP:

```http
POST /api/v1/resource-libraries/health-check
```

参数：

| 参数 | 默认值 | 说明 |
| --- | --- | --- |
| `-libraries` | 空 | 逗号分隔资源库名；空表示全部 |
| `-write-probe` | `false` | 显式上传、读取、删除一个小探针 |
| `-force` | `false` | 跳过本地健康检查冷却时间 |
| `-min-interval` | `0` | 最短检查间隔秒数；0 使用服务端配置 |

### e2e-probe

对指定 route profile 做真实上传、公开读取和清理测试。默认不会保留测试文件。

```powershell
.\bin\supercdnctl.exe e2e-probe -profile china_all
.\bin\supercdnctl.exe e2e-probe -profile china_all -keep
```

HTTP:

```http
POST /api/v1/resource-libraries/e2e-probe
```

参数：

| 参数 | 默认值 | 说明 |
| --- | --- | --- |
| `-profile` | `china_all` | 要测试的线路 |
| `-keep` | `false` | 保留远端文件和本地 DB 记录 |

返回核心字段：

| 字段 | 说明 |
| --- | --- |
| `ok` | 探针是否通过 |
| `upload_latency_ms` | 上传耗时 |
| `read_latency_ms` | 公开读取耗时 |
| `cleanup_remote` | 远端清理状态 |
| `cleanup_db` | 本地 DB 清理状态 |

## 静态资源桶

桶用于管理可复用资源，例如影视海报、高清背景图、Markdown 附件、动态壁纸。桶元数据、索引、用量统计都在本地 SQLite，云盘只保存实际文件和低频目录结构。

公开访问路径：

```text
/a/{bucket_slug}/{logical_path}
```

物理存储路径：

```text
assets/buckets/{bucket_slug}/{type_dir}/{yyyy}/{mm}/{sha_prefix}/{sha256}{ext}
```

### create-bucket

创建桶。

```powershell
.\bin\supercdnctl.exe create-bucket -slug dynamic-wallpapers -name 动态壁纸桶 -profile china_all -types video -max-file-size 314572800 -cache-control "public, max-age=86400"
```

HTTP:

```http
POST /api/v1/asset-buckets
```

参数：

| 参数 | 必填 | 默认值 | 说明 |
| --- | --- | --- | --- |
| `-slug` | 是 | 空 | 桶 slug，用于公开 URL |
| `-name` | 否 | slug | 展示名 |
| `-description` | 否 | 空 | 描述 |
| `-profile` | 否 | `china_all` | 默认线路 |
| `-types` | 否 | 全部类型 | 逗号分隔：`image,video,document,archive,other` |
| `-max-capacity` | 否 | `0` | 桶容量上限字节，0 不限制 |
| `-max-file-size` | 否 | `0` | 单文件上限字节，0 不限制 |
| `-cache-control` | 否 | 线路默认值 | 默认缓存策略 |

### create-cdn-bucket

创建面向海外对象 CDN 的资源桶。它和 `create-bucket` 调用同一个 API，但 CLI 默认值更适合公开静态资源：`-profile overseas_r2`，`-cache-control "public, max-age=31536000, immutable"`。

```powershell
.\bin\supercdnctl.exe create-cdn-bucket -slug overseas-assets -name 海外资源桶 -types image,archive
.\bin\supercdnctl.exe create-cdn-bucket -slug downloads -types archive -cache-control "public, max-age=86400"
```

注意：默认 immutable 缓存适合带版本号或内容 hash 的逻辑路径，例如 `images/v1/poster.jpg` 或 `archives/app-20260429.zip`。会覆盖的固定路径请显式降低 `-cache-control`。

### create-domestic-cdn-bucket

创建面向国内 AList/OpenList 线路的资源桶。默认使用移动线路 `china_mobile`，适合先单线验证；后续可通过 `-line` 切到其它国内线路，或用 `-profile` 显式指定 route profile。

```powershell
.\bin\supercdnctl.exe create-domestic-cdn-bucket -slug mobile-assets -line mobile -types image,document
.\bin\supercdnctl.exe create-domestic-cdn-bucket -slug telecom-downloads -line telecom -types archive -cache-control "public, max-age=86400"
.\bin\supercdnctl.exe create-mobile-cdn-bucket -slug mobile-posters -types image
```

默认值：

| 参数 | 默认值 | 说明 |
| --- | --- | --- |
| `-line` | `mobile` | `mobile` -> `china_mobile`，`telecom` -> `china_telecom`，`unicom` -> `china_unicom`，`all` -> `china_all` |
| `-profile` | 空 | 显式 route profile；传入后覆盖 `-line` |
| `-cache-control` | `public, max-age=86400` | 国内桶默认缓存 1 天，固定路径误缓存风险低于 immutable |

### init-bucket

初始化桶目录结构。

```powershell
.\bin\supercdnctl.exe init-bucket -bucket dynamic-wallpapers -dry-run
.\bin\supercdnctl.exe init-bucket -bucket dynamic-wallpapers
```

HTTP:

```http
POST /api/v1/asset-buckets/{slug}/init
```

参数：

| 参数 | 必填 | 说明 |
| --- | --- | --- |
| `-bucket` | 是 | 桶 slug |
| `-dry-run` | 否 | 只返回计划，不创建目录 |

### upload-bucket

上传桶对象。

```powershell
.\bin\supercdnctl.exe upload-bucket -bucket dynamic-wallpapers -file .\wallpaper.mp4 -path dynamic/20260426T195744Z-01.mp4 -asset-type video -cache-control "public, max-age=86400"
.\bin\supercdnctl.exe upload-bucket -bucket overseas-assets -file .\poster.jpg -path images/v1/poster.jpg -asset-type image -warmup
```

HTTP:

```http
POST /api/v1/asset-buckets/{slug}/objects
```

multipart 字段：

| 字段 | 来源参数 | 必填 | 说明 |
| --- | --- | --- | --- |
| `file` | `-file` | 是 | 本地文件 |
| `path` | `-path` | 是 | 桶内逻辑路径 |
| `asset_type` | `-asset-type` | 否 | 显式指定类型 |
| `cache_control` | `-cache-control` | 否 | 覆盖缓存策略 |

CLI 额外参数：

| 参数 | 必填 | 默认值 | 说明 |
| --- | --- | --- | --- |
| `-warmup` | 否 | `false` | 上传成功后立即调用 `warmup-bucket` 同一对象 |
| `-warmup-method` | 否 | `HEAD` | `HEAD` 或 `GET` |
| `-warmup-base-url` | 否 | `server.public_base_url` | 覆盖预热 URL 的公开域名 |

返回核心字段：

| 字段 | 说明 |
| --- | --- |
| `bucket_object.logical_path` | 逻辑路径 |
| `bucket_object.physical_key` | 物理存储 key |
| `bucket_object.sha256` | 内容 SHA256 |
| `object.id` | 底层对象 ID |
| `url` | 相对公开路径 |
| `public_url` | 绝对公开链接，依赖 `server.public_base_url` |
| `cdn_url` | 存储后端可提供 HTTP 直链时返回的 CDN/存储公开链接 |
| `storage_url` | 同 `cdn_url`，用于明确这是底层存储公开 URL |
| `urls` | 可直接复制的公开链接数组 |

使用 `-warmup` 时输出为 `{ "upload": {...}, "warmup": {...} }`，其中 `upload.public_url` 是上传后的可访问链接。

国内 AList/OpenList 桶会返回稳定的 Super CDN `/a/...` `public_url`。当底层存储能生成签名直链时也会返回 `cdn_url` / `storage_url`；部分下游网盘对重定向后的 `HEAD` 返回 403，直接链路验证应使用 `GET` 或 Range `GET`。

### list-bucket

列出桶对象。

```powershell
.\bin\supercdnctl.exe list-bucket -bucket dynamic-wallpapers
.\bin\supercdnctl.exe list-bucket -bucket dynamic-wallpapers -prefix dynamic/ -limit 20
```

HTTP:

```http
GET /api/v1/asset-buckets/{slug}/objects?prefix=dynamic/&limit=20
```

参数：

| 参数 | 必填 | 默认值 | 说明 |
| --- | --- | --- | --- |
| `-bucket` | 是 | 空 | 桶 slug |
| `-prefix` | 否 | 空 | 逻辑路径前缀 |
| `-limit` | 否 | `100` | 返回数量，最大 1000 |

### purge-bucket

按桶对象公开 URL 调用 Cloudflare purge。对象选择必须显式传 `-path`、`-paths`、`-prefix` 或 `-all`，避免误清整桶。

```powershell
.\bin\supercdnctl.exe purge-bucket -bucket dynamic-wallpapers -path dynamic/20260426T195744Z-01.mp4 -dry-run
.\bin\supercdnctl.exe purge-bucket -bucket dynamic-wallpapers -prefix dynamic/ -dry-run
.\bin\supercdnctl.exe purge-bucket -bucket dynamic-wallpapers -all -cloudflare-account cf_business_main
```

HTTP:

```http
POST /api/v1/asset-buckets/{slug}/purge
```

参数：

| 参数 | 必填 | 默认值 | 说明 |
| --- | --- | --- | --- |
| `-bucket` | 是 | 空 | 桶 slug |
| `-path` | 否 | 空 | 单个逻辑路径 |
| `-paths` | 否 | 空 | 逗号分隔多个逻辑路径 |
| `-prefix` | 否 | 空 | 按逻辑路径前缀选择对象 |
| `-all` | 否 | `false` | 选择桶内全部已跟踪对象 |
| `-limit` | 否 | `0` | prefix 选择的最大对象数；0 由服务端默认到 1000 |
| `-base-url` | 否 | `server.public_base_url` | 覆盖生成 `/a/{bucket}/...` URL 的公开域名 |
| `-cloudflare-account` | 否 | 按 base URL 域名或默认账号 | 指定 Cloudflare 账号 |
| `-cloudflare-library` | 否 | 默认库 | 指定 Cloudflare 资源库 |
| `-dry-run` | 否 | `false` | 只生成 URL，不调用 Cloudflare |

### warmup-bucket

按桶对象公开 URL 发起预热/探测请求。默认使用 `HEAD`，只验证公开 URL 和边缘链路；需要真正拉取对象进入缓存时显式传 `-method GET`。

```powershell
.\bin\supercdnctl.exe warmup-bucket -bucket dynamic-wallpapers -path dynamic/20260426T195744Z-01.mp4 -dry-run
.\bin\supercdnctl.exe warmup-bucket -bucket dynamic-wallpapers -prefix dynamic/ -method HEAD
.\bin\supercdnctl.exe warmup-bucket -bucket dynamic-wallpapers -path dynamic/20260426T195744Z-01.mp4 -method GET
```

HTTP:

```http
POST /api/v1/asset-buckets/{slug}/warmup
```

参数同 `purge-bucket`，另外支持：

| 参数 | 必填 | 默认值 | 说明 |
| --- | --- | --- | --- |
| `-method` | 否 | `HEAD` | `HEAD` 或 `GET`；`GET` 会读取完整响应体 |

### delete-bucket-object

删除桶内一个逻辑对象。默认先删除远端副本，远端删除成功后再删本地 DB 索引。

```powershell
.\bin\supercdnctl.exe delete-bucket-object -bucket dynamic-wallpapers -path dynamic/20260426T195744Z-01.mp4
```

HTTP:

```http
DELETE /api/v1/asset-buckets/{slug}/objects?path=dynamic%2F20260426T195744Z-01.mp4&delete_remote=true
```

参数：

| 参数 | 必填 | 默认值 | 说明 |
| --- | --- | --- | --- |
| `-bucket` | 是 | 空 | 桶 slug |
| `-path` | 是 | 空 | 桶内逻辑路径 |
| `-delete-remote` | 否 | `true` | 是否删除远端副本 |

返回核心字段：

| 字段 | 说明 |
| --- | --- |
| `deleted_local` | 本地对象和索引是否删除 |
| `remote[].target` | 被清理的存储目标 |
| `remote[].status` | `deleted`、`not_found` 或 `error` |
| `errors` | 错误列表 |

注意：

- `-delete-remote=false` 只删除本地索引，会让远端文件变成未跟踪对象，通常只用于灾难恢复。
- 如果远端删除失败，服务不会删除本地 DB 记录，方便重试。

### delete-bucket

删除整个桶。空桶可直接删除；非空桶必须传 `-force` 或 `-delete-objects`。

```powershell
.\bin\supercdnctl.exe delete-bucket -bucket dynamic-wallpapers -force
```

HTTP:

```http
DELETE /api/v1/asset-buckets/{slug}?force=true&delete_objects=true&delete_remote=true
```

参数：

| 参数 | 必填 | 默认值 | 说明 |
| --- | --- | --- | --- |
| `-bucket` | 是 | 空 | 桶 slug |
| `-force` | 否 | `false` | 等价于允许删除非空桶并删除其已跟踪对象 |
| `-delete-objects` | 否 | `false` | 删除桶内已跟踪对象 |
| `-delete-remote` | 否 | `true` | 删除每个对象的远端副本 |

行为：

- 非空桶未传 `-force` 或 `-delete-objects` 时返回冲突错误，不删除任何对象。
- 删除非空桶时，只删除 Super CDN DB 中已跟踪的对象，不递归删除云盘中未跟踪文件。
- 远端对象全部删除成功后，才删除桶记录和 `bucket:{slug}` 项目记录。

## 任务和副本

### job

查询普通异步任务。

```powershell
.\bin\supercdnctl.exe job -id 1
```

HTTP:

```http
GET /api/v1/jobs/{id}
```

### replicas

查询对象副本状态。

```powershell
.\bin\supercdnctl.exe replicas -object-id 5
```

HTTP:

```http
GET /api/v1/objects/{id}/replicas
```

## Cloudflare

### Worker edge proxy

Worker 入口在 `worker/src/index.ts`。它会先请求 Go origin；如果 origin 返回带 `X-SuperCDN-Redirect: storage` 的 30x，Worker 才跟随存储直链，并把内容以原站点 URL 返回。普通业务跳转不会被吞掉。

生产回退需要同时配置：

- Worker secret: `EDGE_BYPASS_SECRET`
- Go origin: `cloudflare.edge_bypass_secret`

当存储直链失败且密钥匹配时，Worker 会用 `X-SuperCDN-Origin-Delivery: origin` 请求 origin 直出文件。`Range` 请求不进边缘缓存。

### sync-site-dns

为站点已绑定域名同步 Cloudflare DNS 记录。默认使用 `cloudflare.site_dns_target` 作为目标，目标是域名时自动使用 `CNAME`，目标是 IPv4/IPv6 时自动使用 `A`/`AAAA`，并默认开启 Cloudflare 代理。

```powershell
.\bin\supercdnctl.exe sync-site-dns -site blog -dry-run
.\bin\supercdnctl.exe sync-site-dns -site blog
.\bin\supercdnctl.exe sync-site-dns -site blog -domains blog.sites.qwk.ccwu.cc -target qwk.ccwu.cc -type CNAME -force
.\bin\supercdnctl.exe sync-site-dns -site blog -proxied=false
```

HTTP:

```http
POST /api/v1/sites/{id}/dns
```

参数：

| 参数 | 必填 | 默认值 | 说明 |
| --- | --- | --- | --- |
| `-site` | 是 | 空 | 站点 ID |
| `-domains` | 否 | 全部已绑定域名 | 只同步指定域名，必须已绑定到该站点 |
| `-cloudflare-account` | 否 | 按域名匹配 | 指定 Cloudflare 账号名 |
| `-cloudflare-library` | 否 | 默认库 | 指定 Cloudflare 加速资源库 |
| `-target` | 否 | `cloudflare.site_dns_target` / public base host / root domain | DNS 记录目标 |
| `-type` | 否 | 按 target 推断 | `A`、`AAAA` 或 `CNAME` |
| `-proxied` | 否 | `true` | 是否开启 Cloudflare 代理 |
| `-ttl` | 否 | `1` | DNS TTL，`1` 表示自动 |
| `-dry-run` | 否 | `false` | 只返回计划，不创建/更新记录 |
| `-force` | 否 | `false` | 允许更新同类型但内容或代理状态不同的记录 |

同名不同类型记录不会被自动覆盖，特别是 `CNAME` 和其他记录冲突时需要人工处理。

### sync-worker-routes

为站点已绑定域名同步 Cloudflare Worker routes。默认只创建缺失的 `{domain}/*` route；如果同 pattern 已指向其他 Worker，会返回 `conflict`，除非传 `-force`。

```powershell
.\bin\supercdnctl.exe sync-worker-routes -site blog -dry-run
.\bin\supercdnctl.exe sync-worker-routes -site blog
.\bin\supercdnctl.exe sync-worker-routes -site blog -domains blog.sites.qwk.ccwu.cc -force
```

HTTP:

```http
POST /api/v1/sites/{id}/worker-routes
```

参数：

| 参数 | 必填 | 默认值 | 说明 |
| --- | --- | --- | --- |
| `-site` | 是 | 空 | 站点 ID |
| `-domains` | 否 | 全部已绑定域名 | 只同步指定域名，必须已绑定到该站点 |
| `-cloudflare-account` | 否 | 按域名匹配 | 指定 Cloudflare 账号名 |
| `-cloudflare-library` | 否 | 默认库 | 指定 Cloudflare 加速资源库 |
| `-script` | 否 | `cloudflare.worker_script` 或 `supercdn-edge` | Worker 脚本名 |
| `-dry-run` | 否 | `false` | 只返回计划，不创建/更新 route |
| `-force` | 否 | `false` | 允许覆盖已指向其他脚本的同 pattern route |

注意：Cloudflare Worker routes 只会在 DNS 记录开启代理时生效；DNS-only 记录会绕过 Worker。

### purge-site

按站点当前 active deployment，或指定 deployment 的 manifest，生成精确 URL 并调用 Cloudflare purge。`index.html` 会同时清 `/` 和 `/index.html`，嵌套 `*/index.html` 会额外清目录 URL。

```powershell
.\bin\supercdnctl.exe purge-site -site blog -dry-run
.\bin\supercdnctl.exe purge-site -site blog
.\bin\supercdnctl.exe purge-site -site blog -deployment dpl-abc -dry-run
```

HTTP:

```http
POST /api/v1/sites/{id}/purge
POST /api/v1/sites/{id}/deployments/{deployment}/purge
```

参数：

| 参数 | 必填 | 默认值 | 说明 |
| --- | --- | --- | --- |
| `-site` | 是 | 空 | 站点 ID |
| `-deployment` | 否 | 当前 active production deployment | 指定部署 ID |
| `-cloudflare-account` | 否 | 按域名匹配 | 指定 Cloudflare 账号名 |
| `-cloudflare-library` | 否 | 默认库 | 指定 Cloudflare 加速资源库 |
| `-dry-run` | 否 | `false` | 只生成 URL，不调用 Cloudflare |

### cloudflare-status

只读诊断 Cloudflare 对接状态，不会创建、修改或删除任何 Cloudflare 资源。

```powershell
.\bin\supercdnctl.exe cloudflare-status
.\bin\supercdnctl.exe cloudflare-status -all
.\bin\supercdnctl.exe cloudflare-status -account cf_business_main
```

HTTP:

```http
GET /api/v1/cloudflare/status
```

检查内容：

- API token 是否有效
- zone 元数据
- 根域名、站点 wildcard 等 DNS 记录
- Worker routes
- R2 buckets（需要 `cloudflare.account_id`）
- 当 `cloudflare_accounts[].r2` 已配置时，R2 状态会额外检查配置的 bucket 是否存在、bucket CORS 规则，以及 `public_base_url` 是否匹配该 bucket 下的 R2 custom domain 或启用的 r2.dev managed domain。若 `cloudflare_accounts[].r2.api_token` 已配置，R2 bucket/CORS/domain 控制面调用会使用它；DNS、Worker、zone 仍使用账号级 `api_token`。

### provision-cloudflare-r2

创建或复用 Cloudflare R2 bucket，并在 bucket 可用后同步 CORS 和公开域名。命令默认 dry-run；执行真实创建需要显式传 `-dry-run=false`。

```powershell
.\bin\supercdnctl.exe provision-cloudflare-r2 -cloudflare-library overseas_accel
.\bin\supercdnctl.exe provision-cloudflare-r2 -cloudflare-library overseas_accel -dry-run=false
.\bin\supercdnctl.exe provision-cloudflare-r2 -cloudflare-account cf_business_main -bucket supercdn-overseas-accel -public-base-url https://overseas-accel.r2.qwk.ccwu.cc -dry-run=false
```

HTTP:

```http
POST /api/v1/cloudflare/r2/provision
Authorization: Bearer <token>
Content-Type: application/json
```

主要参数：

| 参数 | 必填 | 默认 | 说明 |
| --- | --- | --- | --- |
| `-cloudflare-account` | 否 | 默认账号 | 指定 Cloudflare 账号 |
| `-cloudflare-library` | 否 | 默认资源库 | 指定 Cloudflare 资源库，按绑定账号批量创建 |
| `-all` | 否 | `false` | 对全部 Cloudflare 账号执行 |
| `-bucket` | 否 | `supercdn-{library}` | R2 bucket 名，支持 `{account}`、`{library}`、`{root}` 模板 |
| `-public-base-url` | 否 | `https://{library}.r2.{root_domain}` | R2 公开访问 URL，支持同样模板 |
| `-location-hint` | 否 | 空 | R2 location hint |
| `-jurisdiction` | 否 | 空 | R2 jurisdiction 请求头值 |
| `-storage-class` | 否 | 空 | R2 storage class |
| `-dry-run` | 否 | `true` | 只规划不写入 Cloudflare |
| `-cors` | 否 | `true` | 创建/复用后同步 CORS |
| `-domain` | 否 | `true` | 创建/复用后同步公开域名 |
| `-force` | 否 | `false` | 替换不同 CORS 或更新未激活 custom domain |

说明：这个命令只处理 Cloudflare 控制面。账号要真正作为 Super CDN 存储挂载点，还需要在 `cloudflare_accounts[].r2` 配置 S3 兼容的 `access_key_id` 和 `secret_access_key`。

### create-r2-credentials

创建 Cloudflare Account API Token，并按 R2 S3 规则把 token `id` 作为 `access_key_id`、把一次性 token `value` 的 SHA256 作为 `secret_access_key` 写入本地配置。命令默认 dry-run；真实创建必须同时传 `-dry-run=false` 和 `-write-config`，避免一次性 secret 丢失。

```powershell
.\bin\supercdnctl.exe create-r2-credentials -cloudflare-account cf_business_main
.\bin\supercdnctl.exe create-r2-credentials -cloudflare-account cf_business_main -write-config .\configs\config.local.json -dry-run=false
```

主要参数：

| 参数 | 必填 | 默认 | 说明 |
| --- | --- | --- | --- |
| `-cloudflare-account` | 否 | 默认账号 | 指定 Cloudflare 账号 |
| `-cloudflare-library` | 否 | 默认资源库 | 按资源库绑定账号创建 |
| `-all` | 否 | `false` | 对全部 Cloudflare 账号执行 |
| `-bucket` | 否 | 配置或 provision 默认 bucket | R2 bucket 名 |
| `-jurisdiction` | 否 | `default` | R2 scoped resource jurisdiction |
| `-token-name` | 否 | 自动生成 | Account API Token 名，支持 `{account}`、`{library}`、`{root}` |
| `-permission-group` | 否 | `Workers R2 Storage Bucket Item Write` | Cloudflare permission group 名 |
| `-write-config` | 否 | 空 | 写入一次性凭证的本地配置文件 |
| `-dry-run` | 否 | `true` | 只规划不创建 token |
| `-force` | 否 | `false` | 已有 R2 凭证时仍创建替换凭证 |

需要 Cloudflare token 拥有 Account API Tokens 相关权限，否则会返回 Cloudflare `9109 Unauthorized`。

### set-r2-credentials

导入已在 Cloudflare 控制台或其他渠道创建的 R2 S3 凭证，只修改本地配置，不访问 Super CDN 服务端，也不访问 Cloudflare。

```powershell
.\bin\supercdnctl.exe set-r2-credentials -config .\configs\config.local.json -cloudflare-account cf_business_main -access-key-id <id> -secret-access-key <secret>
```

### sync-cloudflare-r2

按 `cloudflare_accounts[].r2` 配置同步 R2 bucket 的 CORS 和公开域名。命令默认 dry-run；执行真实修改需要显式传 `-dry-run=false`。

```powershell
.\bin\supercdnctl.exe sync-cloudflare-r2 -cloudflare-account cf_business_main
.\bin\supercdnctl.exe sync-cloudflare-r2 -cloudflare-library overseas_accel
.\bin\supercdnctl.exe sync-cloudflare-r2 -all -dry-run=false
.\bin\supercdnctl.exe sync-cloudflare-r2 -cloudflare-account cf_business_main -force -dry-run=false
```

HTTP:

```http
POST /api/v1/cloudflare/r2/sync
```

参数：

| 参数 | 必填 | 默认值 | 说明 |
| --- | --- | --- | --- |
| `-cloudflare-account` | 否 | 默认账号 | 指定单个 Cloudflare 账号 |
| `-cloudflare-library` | 否 | 空 | 同步某个 Cloudflare 资源库绑定的账号 |
| `-all` | 否 | `false` | 同步全部已配置 R2 的 Cloudflare 账号 |
| `-dry-run` | 否 | `true` | 只生成计划，不修改 Cloudflare |
| `-force` | 否 | `false` | 允许替换已有不同 CORS 策略或更新非 active custom domain |
| `-cors` | 否 | `true` | 是否同步 bucket CORS |
| `-domain` | 否 | `true` | 是否同步 R2 public domain |
| `-cors-origins` | 否 | `*` | 逗号分隔允许来源；为空时允许任意来源 |
| `-cors-methods` | 否 | `GET,HEAD` | 逗号分隔允许方法 |
| `-cors-headers` | 否 | `*` | 逗号分隔允许请求头 |
| `-cors-expose` | 否 | `ETag,Content-Length,Content-Type,Cache-Control` | 逗号分隔暴露响应头 |
| `-cors-max-age` | 否 | `86400` | CORS max-age 秒数 |

公开域名同步规则：如果 `public_base_url` 匹配该 bucket 的 r2.dev managed domain，则启用 managed domain；否则按 `public_base_url` host 创建或更新 R2 custom domain。创建 custom domain 需要账号配置里有 `zone_id`。

### purge

调用 Cloudflare API 清理缓存。

```powershell
.\bin\supercdnctl.exe purge -urls https://cdn.example.com/a/file.css,https://cdn.example.com/a/file.js
```

HTTP:

```http
POST /api/v1/cache/purge
```

参数：

| 参数 | 必填 | 说明 |
| --- | --- | --- |
| `-urls` | 是 | 逗号分隔完整 URL |

前置配置：

- `cloudflare.account_id`（R2/账号级诊断需要）
- `cloudflare.zone_id`
- `cloudflare.api_token`
