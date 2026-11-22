# Card 2141 — 核对根门面准备失败后的直接分派

Milestone: 稳定模块第 9 节复审
Recipe: specs/change-recipes/analysis-only.yaml
Context Package: review
Priority: P3
State: done
Primary Module: .
Owned Files: `plumego_test.go`
Depends On: 无

Goal: 核对根门面准备失败后的直接分派。
Scope: 通过根门面创建配置无效的 App，用公开入口记录 Prepare、注册状态和直接 ServeHTTP 的结果。
Non-goals: 本卡提交取证结果。后续行为变更由独立修复卡执行。
Files: `plumego_test.go`
Tests: 保留 App 所拥有的配置验证。核对路由命中、参数、Prepare 失败及直接分派导致的处理阶段变化。
Docs Sync: 核对既有文档与验收结果一致，记录需要后续同步的位置。
Done Definition: 记录命令、结果、证据限制和是否需要独立修复卡。
Outcome: 根门面使用无效监听地址时 Prepare 返回错误。已注册的处理器通过直接 ServeHTTP 返回正确参数及 200，验证服务准备和直接分派各自的契约。

## 证据与影响

证据状态为待补证。第 9 节检查号为 3,6,14,28,36,37。根门面已有显式零配置和已关闭直调回归。Prepare 因服务层配置失败时已登记路由能否继续直调的组合路径待核对。 [plumego.go:60](../../../plumego.go#L60) [core/http_handler.go:85](../../../core/http_handler.go#L85) [core/http_handler.go:184](../../../core/http_handler.go#L184) [plumego_test.go:194](../../../plumego_test.go#L194)

[复审报告](../../../docs/evidence/stable-quality-review-6e8d53f9.md)

## 验收与验证

- [x] 保留 App 所拥有的配置验证。核对路由命中、参数、Prepare 失败及直接分派导致的处理阶段变化。
- [x] 记录命令、结果、证据限制和是否需要独立修复卡。

```sh
go test -race -timeout 60s .
```
