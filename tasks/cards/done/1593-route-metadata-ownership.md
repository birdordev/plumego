# Card 1593 — 明确 RouteState 在公开上下文中的借用约定

Milestone: 本轮质量审查
Recipe: specs/change-recipes/add-acceptance-tests.yaml
Context Package: implementation
Priority: P2
State: done
Primary Module: contract
Owned Files: `contract/context_core.go`, `contract/context_test.go`, `docs/modules/contract/README.md`, `website/src/generated/modules.ts`
Depends On: 无

Goal: 明确 RouteState 在公开上下文中的借用约定。
Scope: 复核公开 GoDoc 与 router 调用，补足借用和生命周期约定。
Non-goals: 保留现有公开签名、依赖与模块边界。测试若证实新的缺陷，先记录证据再单独安排修复。
Files: `contract/context_core.go`, `contract/context_test.go`, `docs/modules/contract/README.md`, `website/src/generated/modules.ts`。
Tests: 验证已发布状态在请求范围可读，普通参数载体变更不会影响已发布快照。
Docs Sync: 核对 `docs/modules/contract/README.md`。 修改模块说明后检查网站生成差异，并在同一交付中保留相应生成文件。
Done Definition: 完成下列验收和验证，记录证据与剩余风险。
Outcome: 模块说明明确 RouteState 借用和发布时的不可变约定，以现有上下文快照回归验证。

## 证据与影响

- 检查号：16,27,32,34,37。证据状态：补证。
- 路由缓存共享 RouteState，普通 RequestContext 会复制参数。需明确发布后再修改指针的契约。 [contract/context_core.go:105](../../../contract/context_core.go#L105)
- [审查报告](../../../docs/evidence/stable-quality-review-dee7aa0f.md)

## 验收与验证

- [x] 验证已发布状态在请求范围可读，普通参数载体变更不会影响已发布快照。
- [x] 运行以下命令并记录结果、失败路径和未验证范围。修改 Go 文件前完成仓库预检，验证前先执行仓库格式化。

```sh
go test -race -timeout 60s ./contract
make website-sync
make website-check-generated
```

## 实施证据

[TestWithRouteStatePreservesContext](../../../contract/context_test.go#L145) · [TestRequestContextParamsAreCopied](../../../contract/context_test.go#L47)
