# Plumego — 标准库优先的 Go Web 工具包

[![Go 文档](https://pkg.go.dev/badge/github.com/spcent/plumego.svg)](https://pkg.go.dev/github.com/spcent/plumego)
[![Go 版本](https://img.shields.io/badge/Go-1.26%2B-00ADD8?style=flat&logo=go)](https://go.dev/)
[![CI](https://github.com/spcent/plumego/actions/workflows/quality-gates.yml/badge.svg)](https://github.com/spcent/plumego/actions/workflows/quality-gates.yml)
[![Go Report Card](https://goreportcard.com/badge/github.com/spcent/plumego)](https://goreportcard.com/report/github.com/spcent/plumego)
[![状态](https://img.shields.io/badge/status-v1.1.0-blue)](https://github.com/spcent/plumego/releases/tag/v1.1.0)
[![许可证](https://img.shields.io/badge/license-MIT-green)](LICENSE)

Plumego 是一个基于 Go 标准库构建的小型 HTTP 工具包，把 `net/http`
兼容性放在核心位置：处理器仍是普通的
`func(http.ResponseWriter, *http.Request)`，中间件仍包装 `http.Handler`，
应用装配仍显式写在你自己的 `main` 包中。

稳定表面刻意保持收敛。先从 `core`、`router`、`contract` 和 `middleware`
开始；只有在职责需要时，再加入 `security`、`store`、`health`、`log` 和 `metrics`。

## 为什么选择 Plumego

适合需要比原生 `http.ServeMux` 更多结构、但又不想引入大型框架模型的 Go 服务。

**选择 Plumego 如果你：**
- 希望理解 HTTP 服务器装配的每一行代码
- 更喜欢标准库的设计思路和模式
- 期望你的服务能稳定维护多年
- 使用代码代理（Claude、Codex）辅助开发
- 看重小型、可测试、易重构的代码而非便利性

**Plumego 不是：**
- 「Gin/Echo 替代品」（我们互补于标准库，而非与框架竞争）
- 最快的选项（Fiber 追求原始吞吐量；我们优化清晰性）
- 「电池内置」框架（标准库优先是我们的设计）
- 面向零装配代码的框架（如果你想 `plumego.Handlers{ "POST /users": createUser }`，你需要其他框架）

| 工具包 | 定位 | 最佳用途 |
|--------|------|---------|
| `http.ServeMux` | 最小路由 | 学习、微小服务 |
| **Plumego** | **标准库之上的薄层** | **可维护的生产服务** |
| Chi | 轻量路由 | 函数构造器风格 |
| Gin | 快速 + 便利 | 快速原型开发 |
| Echo | 功能丰富 | 全功能应用 |
| Fiber | 高性能 | 最大吞吐量 |

详见 [`docs/start/POSITIONING.md`](./docs/start/POSITIONING.md) 了解设计哲学和何时选择 Plumego。

## 快速开始

`main.go`：

```go
package main

import (
	"log"
	"net/http"

	"github.com/spcent/plumego"
	"github.com/spcent/plumego/contract"
)

func main() {
	app := plumego.New()
	app.Get("/ping", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = contract.WriteResponse(w, r, http.StatusOK, map[string]string{"message": "pong"}, nil)
	}))
	log.Fatal(http.ListenAndServe(":8080", app))
}
```

运行：

```bash
go mod init example.com/hello
go get github.com/spcent/plumego@latest
go run main.go
```

打开 `http://localhost:8080/ping`。完成示例后，再阅读
[`reference/standard-service`](./reference/standard-service) 查看生产风格的规范应用结构。

如需自定义地址、超时或 TLS：

```go
cfg := plumego.DefaultConfig()
cfg.Addr = ":9090"
app := plumego.NewWithConfig(cfg)
```

如需注入日志器或进行完整生产装配，直接使用 `core.New` —— 参见
[`docs/start/getting-started.md`](./docs/start/getting-started.md)。

## 选择你的起点

| 我想构建… | 从这里开始 | 成熟度 |
| --- | --- | --- |
| 普通 JSON API | `reference/standard-service` → 仅使用稳定根包 | **GA** |
| 带 CRUD 约定的 REST 资源 | `reference/with-rest` → `x/rest` | beta |
| 多租户 SaaS API | `reference/with-tenant` → `x/tenant` | beta |
| API 网关或反向代理 | `reference/with-gateway` → `x/gateway` | beta |
| 实时 WebSocket 功能 | `reference/with-websocket` → `x/websocket` | beta |
| AI 驱动的服务 | `reference/with-ai` → `x/ai/provider` | experimental |
| 带消息队列/Webhook 的服务 | `reference/with-messaging` → `x/messaging` | beta |
| gRPC + HTTP 混合服务 | `reference/with-rpc` → `x/rpc` | experimental |
| 可观测性（Prometheus / OpenTelemetry） | `reference/with-observability` → `x/observability` | beta |
| 租户管理控制台 | `reference/with-tenant-admin` → `x/tenant` | beta |

所有路径都以 `reference/standard-service` 为基础结构；扩展包是显式增量，而不是另一套启动框架。

## 为什么选择 plumego

适合需要比原生 `http.ServeMux` 更多结构、但又不想引入大型框架模型的 Go 服务。

| 原则 | plumego 的做法 |
| --- | --- |
| 标准库优先 | 普通 handler、中间件、request、response writer 和 `*http.Server`。 |
| 显式装配 | 路由、中间件、依赖和生命周期都能在构造位置直接看到。 |
| 小型稳定表面 | 稳定根包职责清晰，不是功能目录。 |
| 便于代理维护 | `specs/`、`tasks/` 和每模块 `module.yaml` 让范围与验证方式可发现。 |
| 可选能力 | 产品功能和协议适配放在稳定学习路径之外。 |

## 标准库对比

| 能力 | `http.ServeMux` | plumego |
| --- | --- | --- |
| 基础路由 | 方法处理由调用方自行判断。 | `Get`/`Post`/`AddRoute` 注册一个方法、路径、处理器。 |
| `{param}` 路径提取 | 调用方手动解析路径段。 | 路由器匹配参数，从请求上下文读取。 |
| 路由分组 | 调用方手动重复前缀。 | 分组应用统一前缀。 |
| 分组中间件 | 调用方按子树手动组合。 | 分组携带共享中间件，保持 `http.Handler` 形状。 |
| 命名路由与反向 URL | 调用方手动拼接 URL。 | 通过 app/router API 生成反向 URL。 |
| 路由冻结 | 改装配代码路由就可能变化。 | `Prepare` 在启动前冻结路由。 |
| 结构化错误 | 调用方定义每种响应形状。 | `contract.WriteError` 提供规范错误路径。 |
| Request ID 传递 | 调用方自选并传播约定。 | 显式上下文访问器 + 中间件支持。 |
| 优雅生命周期 | 调用方自行组织启动与关闭。 | `Prepare`、`Server`、`Shutdown` 让生命周期显式且可复用。 |

## 包导览

| 包 | 职责 |
| --- | --- |
| `core` | 应用构造、路由注册、中间件挂载、服务生命周期。 |
| `router` | 路由匹配、路径参数、分组、元数据、反向 URL 生成。 |
| `contract` | 响应写入、结构化错误构造、请求元数据、传输绑定。 |
| `middleware` | 传输层中间件组合和一方中间件包。 |
| `security` | 认证、JWT、密码、安全头、输入安全、防滥用。 |
| `store` | 稳定存储契约与内存原语（缓存、KV、文件、数据库、幂等）。 |
| `health` | 面向应用与依赖状态的健康/就绪模型。 |
| `log` | 最小日志接口和默认日志器。 |
| `metrics` | 最小指标契约（计数器、仪表、耗时、收集器）。 |

可选能力族位于 `x/*` —— 稳定根路径上的显式增量，而不是另一套结构。

## 当前状态

上表中的九个包是**稳定根包**，具备完整的 `v1` 兼容性保证：接口签名、包名称和行为在
`v1.x` 发布系列内不会发生破坏性变更。

七个 `x/*` 扩展族处于 **beta** 状态——在已引用的发布 ref 范围内 API 稳定，适合生产使用，
但边缘情况可能仍不完善：`x/frontend`、`x/gateway`、`x/messaging`、`x/observability`、
`x/rest`、`x/tenant` 和 `x/websocket`。

其余所有 `x/*` 扩展均为 **experimental**：API 可能在任意次要版本中变更，无需事先通知。
未经显式的项目级稳定化处理，请勿在生产服务中依赖这些扩展。

完整兼容性详见 [`STABILITY.md`](./docs/release/STABILITY.md)、版本升级路径见 [`COMPATIBILITY.md`](./docs/release/COMPATIBILITY.md)，
晋级标准见 [`docs/reference/extension-stability-policy.md`](./docs/reference/extension-stability-policy.md)。

## Agent-First Design

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
- [`docs/start/troubleshooting.md`](./docs/start/troubleshooting.md) —— 常见问题和解决方案
- [`docs/evidence/benchmarks/README.md`](./docs/evidence/benchmarks/README.md) —— 与 Chi、Gin、Echo 的性能对比
- [`docs/start/adoption-path.md`](./docs/start/adoption-path.md) —— 5 分钟、30 分钟、1 天采用路径。
- [`docs/start/troubleshooting.md`](./docs/start/troubleshooting.md) —— 路由冻结、中间件顺序、JWT 错误、生命周期问题。
- [`docs/evidence/benchmarks/README.md`](./docs/evidence/benchmarks/README.md) —— 与 Chi、Gin、Echo 的性能对比。

**参与贡献？**
- [`CONTRIBUTING.md`](./CONTRIBUTING.md) —— 开发环境、验证门禁与 PR 流程
- [`SECURITY.md`](./SECURITY.md) —— 支持版本与漏洞私密报告渠道
- [`CHANGELOG.md`](./CHANGELOG.md) —— 版本发布历史
