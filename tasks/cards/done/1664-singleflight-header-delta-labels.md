# Card 1664 — 核对合并回放中外层多值头的所有权

Milestone: 稳定模块质量复审
Recipe: specs/change-recipes/add-acceptance-tests.yaml
Context Package: implementation
Priority: P2
State: done
Primary Module: middleware
Owned Files: `middleware/singleflight/response_regression_test.go`
Depends On: 无

Goal: 核对合并回放中外层多值头的所有权。
Scope: 用三套普通头对照 leader、多个 waiter 和无任何变化的头。
Non-goals: 维持公开签名和依赖；涉及现有兼容契约的调整须先核对调用边界及迁移影响。
Files: `middleware/singleflight/response_regression_test.go`。
Tests: 各 waiter 只保留自己的外层值，再应用共同变化；修改一个结果不影响余者。
Docs Sync: 对照原契约核验，若证据要求修订公开行为，先更新实施范围。
Done Definition: 完成测试与验收记录，并标明剩余取证范围。
Outcome: 两个不同 waiter 各自保留外层 Vary 与 ID，统一应用 leader 的删除、替换与追加。

## 证据

检查号为 24,32,34,39，状态为补证。

回放对附加值、删除值与原有值分别处理。不同等待者带有其自己的中间件头。 [middleware/singleflight/singleflight.go:387](../../../middleware/singleflight/singleflight.go#L387)

[复审报告](../../../docs/evidence/stable-quality-review-09a7f5d3.md)

## 验收与验证

- [x] 各 waiter 只保留自己的外层值，再应用共同变化；修改一个结果不影响余者。
- [x] 记录验证命令、状态和剩余范围。若使用既有用例满足验收，记录对应证据。

```sh
go test -race -timeout 60s ./middleware/singleflight
```

## 实施证据

[TestCoalescedResponsesApplyHeaderChangesToEachWaiter](../../../middleware/singleflight/response_regression_test.go#L319)
