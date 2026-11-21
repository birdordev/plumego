# Card 2074 — 冻结前后 nil option 与元数据快照

Milestone: 稳定模块第 9 节复审
Recipe: specs/change-recipes/add-acceptance-tests.yaml
Context Package: implementation
Priority: P2
State: done
Primary Module: router
Owned Files: `router/tags_regression_test.go`
Depends On: 无

Goal: 冻结前后 nil option 与元数据快照
Scope: 带 nil 和不同标签 option 注册一项，再冻结并尝试等价注册不同路径。
Non-goals: 范围限于本卡涉及的契约，保持公开签名和依赖。
Files: `router/tags_regression_test.go`。
Tests: nil 无副作用，冻结后的命名表、标签和匹配缓存均无新项。
Docs Sync: 核对新增验收与已有模块说明一致，记录证据和限制。
Done Definition: 完成以下验收，记录命令、结果与剩余限制。
Outcome: 在并发快照回归中补 nil option 与冻结后拒绝断言。命名表、标签和分派保留原快照。

## 证据与影响

证据状态为补证。第 9 节检查号为 6,19,33,37。 [质量规则第 9 节](../../../docs/operations/agent-code-quality-rules.md#L267)

注册先处理非 nil options 再发布，冻结后应拒绝整个登记。 [router/registration.go:201](../../../router/registration.go#L201)

[复审报告](../../../docs/evidence/stable-quality-review-2f962d21.md)

## 验收与验证

- [x] nil 无副作用，冻结后的命名表、标签和匹配缓存均无新项。
- [x] 已有测试覆盖相同触发和断言时可直接复用，记录具体用例。

```sh
go test -race -timeout 60s ./router
```

## 实施证据

- [TestRouteTagsAreDetached](../../../router/tags_regression_test.go#L9)
