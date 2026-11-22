# Plumego — 标准库优先的 Go Web 工具包

[![Go 文档](https://pkg.go.dev/badge/github.com/birdordev/plumego.svg)](https://pkg.go.dev/github.com/birdordev/plumego)
[![Go 版本](https://img.shields.io/badge/Go-1.26%2B-00ADD8?style=flat&logo=go)](https://go.dev/)
[![CI](https://github.com/birdordev/plumego/actions/workflows/quality-gates.yml/badge.svg)](https://github.com/birdordev/plumego/actions/workflows/quality-gates.yml)
[![Go Report Card](https://goreportcard.com/badge/github.com/birdordev/plumego)](https://goreportcard.com/report/github.com/birdordev/plumego)
[![codecov](https://codecov.io/gh/birdordev/plumego/graph/badge.svg)](https://codecov.io/gh/birdordev/plumego)
[![状态](https://img.shields.io/badge/status-v1.1.0-blue)](https://github.com/birdordev/plumego/releases/tag/v1.1.0)
[![许可证](https://img.shields.io/badge/license-MIT-green)](LICENSE)

Plumego 是一个基于 Go 标准库构建的小型 HTTP 工具包，把 `net/http`
兼容性放在核心位置：处理器仍是普通的
`func(http.ResponseWriter, *http.Request)`，中间件仍包装 `http.Handler`，
应用装配仍显式写在你自己的 `main` 包中。

稳定表面刻意保持收敛。先从 `core`、`router`、`contract` 和 `middleware`
开始；只有在职责需要时，再加入 `security`、`store`、`health`、`log` 和 `metrics`。

## 为什么选择 Plumego

当 `net/http` 服务需要路由分组、统一的 JSON 响应与错误格式，以及显式的服务生命周期时，
可以使用 Plumego。主 Go 模块没有第三方依赖。可选能力由应用入口显式装配。

## 快速开始

需要 Go 1.26 或更高版本。将下面的示例保存为 `main.go`。

```go
package main

import (
	"log"
	"net/http"

	"github.com/birdordev/plumego"
	"github.com/birdordev/plumego/contract"
)

func main() {
	app := plumego.New()
	if err := app.Get("/ping", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := contract.WriteResponse(w, r, http.StatusOK, map[string]string{"message": "pong"}, nil); err != nil {
			log.Printf("write ping response: %v", err)
		}
	})); err != nil {
		log.Fatal(err)
	}
	log.Fatal(http.ListenAndServe(":8080", app))
}
```

运行：

```bash
go mod init example.com/hello
go get github.com/birdordev/plumego@latest
go run main.go
```

在另一个终端运行 `curl -fsS http://localhost:8080/ping`，JSON 响应的
`data` 中会包含 `"message":"pong"`。

示例中的监听器由 `http.ListenAndServe` 管理，`app` 在首个请求到来时准备处理器。
如需让 `AppConfig` 中的地址、超时和 TLS 配置作用于服务器，使用
`plumego.NewWithConfig` 创建应用，注册路由后调用 `app.Run()`。
如需显式控制生命周期，使用 `Prepare`、`Server` 和 `Shutdown`。
如需注入依赖，使用 `core.New`。完整示例见[入门指南](./docs/start/getting-started.md)。

## 选择你的起点

- **快速上手** · [最小服务](./examples/hello)和[错误处理](./examples/error-handling)。
- **标准应用** · [标准服务](./reference/standard-service)及其 [CRUD 教程](./reference/standard-service/TUTORIAL.md)。
- **功能集成** · [参考应用目录](./reference/README.md#feature-scenario-references)。

| 我想构建… | 从这里开始 | 包成熟度 |
| --- | --- | --- |
| 普通 JSON API | [标准服务](./reference/standard-service) → 仅使用稳定根包 | **GA** |
| 认证接口和请求指标 | [生产参考服务](./reference/production-service) → 稳定根包，租户资料接口使用 `x/tenant` | GA + beta |
| 带 CRUD 约定的 REST 资源 | [`with-rest`](./reference/with-rest) → `x/rest` | beta |
| 多租户 SaaS API | [`with-tenant`](./reference/with-tenant) → `x/tenant` | beta |
| API 网关或反向代理 | [`with-gateway`](./reference/with-gateway) → `x/gateway` | beta |
| 实时 WebSocket 功能 | [`with-websocket`](./reference/with-websocket) → `x/websocket` | beta |
| AI 驱动的服务 | [`with-ai`](./reference/with-ai) → `x/ai/provider`、`session`、`streaming`、`tool` | 子包 beta |
| 应用消息服务 | [`with-messaging`](./reference/with-messaging) → `x/messaging` | beta |
| 事件与发布订阅 | [`with-events`](./reference/with-events) → `x/messaging/pubsub` | experimental |
| 前端与静态资源 | [`with-frontend`](./reference/with-frontend) → `x/frontend` | beta |
| 受保护的运维与管理路由 | [`with-ops`](./reference/with-ops) → `x/observability/ops` | experimental |
| gRPC + HTTP 混合服务 | [`with-rpc`](./reference/with-rpc) → `x/rpc` | experimental |
| 可观测性（Prometheus / OpenTelemetry） | [`with-observability`](./reference/with-observability) → `x/observability` | beta |
| 租户管理控制台 | [`with-tenant-admin`](./reference/with-tenant-admin) → `x/tenant` | beta |
| Webhook 接收与投递 | [`with-webhook`](./reference/with-webhook) → `x/messaging/webhook` | experimental |
| HTTP 文件上传与下载 | [文件 API 导读](./docs/modules/x/fileapi/README.md) → `x/fileapi` | experimental |
| SQL 写入、迁移与事务发件箱 | [SQL 示例](./reference/with-sql) | 独立示例 |
| 浏览器表单、会话与 OIDC | [浏览器示例](./reference/with-browser) | 独立示例 |
| SSE 推送 | [SSE 示例](./reference/with-sse) | 独立示例 |

参考应用以 `reference/standard-service` 的装配方式为基础，扩展包在应用入口显式接入。

## 标准库对比

| 能力 | `http.ServeMux` | plumego |
| --- | --- | --- |
| 基础路由 | `Handle` 和 `HandleFunc` 支持 `GET /users` 等带方法的模式。 | `Get`/`Post`/`AddRoute` 注册方法、路径和处理器。 |
| 路径参数 | 匹配 `/users/{id}`，用 `r.PathValue("id")` 读取。 | 匹配 `/users/:id`，用 `plumego.Param(r, "id")` 读取。 |
| 路由分组 | 注册带前缀的模式，或挂载子路由器。 | 分组应用统一前缀。`App.Mount` 将前缀及其子路径交给指定处理器。 |
| 分组中间件 | 调用方按子树手动组合。 | 通过 `Group.Use` 在注册路由前装配组内共享中间件。 |
| 命名路由与反向 URL | 调用方手动拼接 URL。 | 通过 app/router API 生成反向 URL。 |
| 路由冻结 | 开始提供服务后仍可注册路由。 | `Prepare` 可在启动前冻结路由。直接调用 `ServeHTTP` 时，首次准备处理器也会冻结路由。 |
| 结构化错误 | 调用方定义每种响应形状。 | `contract.WriteError` 提供规范错误路径。 |
| Request ID 传递 | 调用方自选并传播约定。 | 显式上下文访问器 + 中间件支持。 |
| 优雅生命周期 | 调用方自行组织启动与关闭。 | `RunContext` 启动 HTTP 并排空在途请求。`Prepare`、`Server` 和 `Shutdown` 支持调用方管理监听器。 |

## 包导览

| 包 | 职责 |
| --- | --- |
| `core` | 应用构造、路由注册、中间件挂载、服务生命周期。 |
| `router` | 路由匹配、路径参数、分组、元数据、反向 URL 生成。 |
| `contract` | 响应写入、结构化错误构造、请求元数据、传输绑定。 |
| `middleware` | 传输层中间件组合及框架提供的中间件。 |
| `security` | 认证、JWT、密码、安全头、输入安全、防滥用。 |
| `store` | 文件和幂等契约、数据库辅助工具、内存缓存以及小型嵌入式 KV 存储。 |
| `health` | 面向应用与依赖状态的健康/就绪模型。 |
| `log` | 最小日志接口和默认日志器。 |
| `metrics` | 最小指标契约（计数器、仪表、耗时、收集器）。 |

可选能力位于 `x/*`，由应用根据需要显式装配。

## 性能与 I/O

- 应用复用已准备好的处理器链，冻结后的路由器直接查找静态路由。详见[路由器导读](./docs/modules/router/README.md)。
- 文件型 `store/kv` 的并发写入可共用一次快照替换及同步。每次提交仍写入全量数据。详见 [KV 边界](./docs/modules/store/README.md#kv-boundary)。
- 网关会与支持 HTTP/2 的 HTTPS 上游协商协议，并随上游数据到达逐步刷新 SSE 响应。详见[网关导读](./docs/modules/x/gateway/README.md)。

运行当前检出版本的路由和应用微基准。

```bash
go test ./router ./core -run '^$' -bench 'Benchmark(Opt|AppServeHTTP)' -benchmem -count=3 -cpu=1,4,8
```

[v1.1.0 对比结果](./docs/evidence/benchmarks/results-v1.1.0.md)对应已打标签的版本。
高并发下的端到端延迟应通过业务负载测试测量。

## 当前状态

上表中的九个包是**稳定根包**，公开 API 和已记录的行为享有 `v1.x` 兼容性保证。

七个 `x/*` 扩展族的入口为 **beta**，并在所列发布版本间有兼容性证据。它们是
`x/frontend`、`x/gateway`、`x/messaging`、`x/observability`、`x/rest`、
`x/tenant` 和 `x/websocket`。

`x/ai` 和 `x/data` 等实验性扩展族中有已达 beta 的子包，部分 beta 扩展族中也有
实验性子包。请按实际导入路径查看[扩展成熟度清单](./docs/concepts/extension-maturity.md)。
实验性 API 可能在次要版本中变更。

完整保证见[稳定性承诺](./docs/release/STABILITY.md)，升级路径见[兼容性指南](./docs/release/COMPATIBILITY.md)，
晋级条件见[扩展稳定性政策](./docs/reference/extension-stability-policy.md)。

## 面向代码代理的设计

Plumego 使用 agent-first 控制面维护仓库：`docs/` 解释架构，`specs/`
记录可机器检查的边界，`tasks/` 定义可审查的执行单元，`reference/`
展示规范装配方式。`internal/checks/` 下的检查在本地和 CI 执行关键边界验证，
让自动化修改带着可审查证据进入 review，而不依赖隐含约定。完整模型与采用路径见
[`docs/concepts/agent-first.md`](./docs/concepts/agent-first.md)，详细内部操作参考见
[`docs/operations/agent-first-operating-reference.md`](./docs/operations/agent-first-operating-reference.md)。

## 获取帮助

**初次使用？**

- [`docs/start/POSITIONING.md`](./docs/start/POSITIONING.md) —— 为什么选择 Plumego 以及何时使用
- [`docs/start/getting-started.md`](./docs/start/getting-started.md) —— 最小可运行教程
- [`docs/start/adoption-path.md`](./docs/start/adoption-path.md) —— 5 分钟、30 分钟、1 天的采用路径

**构建应用？**

- [`reference/standard-service`](./reference/standard-service) —— 规范应用结构
- [`docs/reference/canonical-style-guide.md`](./docs/reference/canonical-style-guide.md) —— 处理器、中间件、路由约定
- [`docs/reference/reference-apps.md`](./docs/reference/reference-apps.md) —— 参考应用选择指南

**选择技术？**

- [`STABILITY.md`](./docs/release/STABILITY.md) —— 哪些内容在 v1 中稳定
- [`COMPATIBILITY.md`](./docs/release/COMPATIBILITY.md) —— 版本升级和迁移路径
- [`docs/modules`](./docs/modules) —— 各包模块导读

**遇到问题？**

- [排障指南](./docs/start/troubleshooting.md)涵盖路由冻结、中间件顺序、JWT 错误和生命周期问题。
- [性能基准](./docs/evidence/benchmarks/README.md)记录了发布版对比结果及复现方式。

**参与贡献？**

- [`CONTRIBUTING.md`](./CONTRIBUTING.md) —— 开发环境、验证门禁与 PR 流程
- [`SECURITY.md`](./SECURITY.md) —— 支持版本与漏洞私密报告渠道
- [`CHANGELOG.md`](./CHANGELOG.md) —— 版本发布历史
