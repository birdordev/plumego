# Plumego Extensions (`x/*`)

扩展包与稳定根同属主 Go 模块，默认只使用 Go 标准库。新增第三方依赖需要明确批准，`x/*` 不设独立的 `go.mod`。详见[仓库规则](../AGENTS.md#2-non-negotiables)。

## 扩展家族成熟度

家族状态以各自的 `module.yaml` 为准。子包和专项接口可以有独立的成熟度与发布证据，见[扩展成熟度模型](../docs/concepts/extension-maturity.md)和[Beta 证据清单](../specs/extension-beta-evidence.yaml)。

| 扩展家族 | 状态 | 使用指南 |
|---|---|---|
| [ai](ai/module.yaml) | Experimental | [AI 扩展](../docs/modules/x/ai/README.md) |
| [data](data/module.yaml) | Experimental | [数据扩展](../docs/modules/x/data/README.md) |
| [fileapi](fileapi/module.yaml) | Experimental | [文件 API](../docs/modules/x/fileapi/README.md) |
| [frontend](frontend/module.yaml) | Beta | [前端静态资源](../docs/modules/x/frontend/README.md) |
| [gateway](gateway/module.yaml) | Beta | [网关与代理](../docs/modules/x/gateway/README.md) |
| [messaging](messaging/module.yaml) | Beta | [消息服务](../docs/modules/x/messaging/README.md) |
| [observability](observability/module.yaml) | Beta | [可观测性](../docs/modules/x/observability/README.md) |
| [openapi](openapi/module.yaml) | Experimental | [OpenAPI 生成](../docs/modules/x/openapi/README.md) |
| [resilience](resilience/module.yaml) | Experimental | [熔断与限流](../docs/modules/x/resilience/README.md) |
| [rest](rest/module.yaml) | Beta | [REST 资源](../docs/modules/x/rest/README.md) |
| [rpc](rpc/module.yaml) | Experimental | [RPC 适配器](../docs/modules/x/rpc/README.md) |
| [tenant](tenant/module.yaml) | Beta | [租户能力](../docs/modules/x/tenant/README.md) |
| [validate](validate/module.yaml) | Experimental | [请求绑定](../docs/modules/x/validate/README.md) |
| [websocket](websocket/module.yaml) | Beta | [WebSocket](../docs/modules/x/websocket/README.md) |

## 导入约束

- 稳定根（`core`, `router`, `contract`, ...）**禁止**导入 `x/*`
- `x/*` 之间导入需遵循 [依赖规则](../specs/dependency-rules.yaml)
- `reference/*` 可自由选择所需扩展

## 快速开始

参考应用展示每个扩展的推荐集成方式：

| 场景 | 参考应用 |
|---|---|
| LLM/AI 集成 | `reference/with-ai` |
| 多租户 API | `reference/with-tenant` |
| WebSocket 实时通信 | `reference/with-websocket` |
| gRPC / RPC | `reference/with-rpc` |
| 网关/代理 | `reference/with-gateway` |
| 消息队列 | `reference/with-messaging` |
| 可观测性 | `reference/with-observability` |
| REST CRUD | `reference/with-rest` |
