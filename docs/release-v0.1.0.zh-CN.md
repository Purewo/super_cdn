# Super CDN v0.1.0

[English](release-v0.1.0.md) | 简体中文

发布日期：2026-05-13

状态：内部稳定基线。

v0.1.0 是 Super CDN 的第一个内部稳定点，重点是把控制面、用户权限、静态对象上传、基础部署和运维入口固定下来。

## 包含内容

- Go 控制面服务和 `supercdnctl` 命令行入口。
- SQLite 元数据：项目、对象、副本、站点、部署、用户和 token。
- 基础用户模型：root/admin、owner、maintainer、viewer。
- 邀请登录、本地 CLI profile 和 `whoami`。
- `doctor` 作为第一份支持报告。
- 普通静态对象 `/o/{project}/{path}` 上传和读取。
- 初始站点部署、生产指针和部署历史。
- Cloudflare/R2/AList/OpenList/IPFS 后续集成的配置承载结构。

## 角色模型

管理员 token 仍是 break-glass 凭证。团队成员应通过 invite token 登录，并把个人 token 保存到本地 profile。Owner 负责邀请和 token 管理，maintainer 负责日常创建资源和部署，viewer 只读。

## 生产部署

v0.1.0 的生产含义是“内部可用基线”，不是公开 GA。它适合继续构建资源桶、Web 托管和 provider 集成，但不应承诺所有 provider 写入路径都已经成熟。

## 验证

发布前应确认：

- Go 测试通过；
- CLI 基础命令可运行；
- 本地服务 `/healthz` 可访问；
- invite/login/whoami/doctor 流程可用；
- 静态对象上传和读取可用；
- README 和基础 handoff 文档指向正确。

## 已知边界

- provider 写入、回滚、Cloudflare Static、hybrid edge 和智能路由仍属于后续版本。
- 真实公网 canary 覆盖有限。
- CI、OpenAPI lint、审计事件和完整文档检查尚未达到后续版本标准。
