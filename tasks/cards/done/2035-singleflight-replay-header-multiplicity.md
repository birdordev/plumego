# Card 2035 — 合流重放保留重复响应头

Milestone: 稳定模块第 9 节复审
Recipe: specs/change-recipes/add-acceptance-tests.yaml
Context Package: implementation
Priority: P2
State: done
Primary Module: middleware/singleflight
Owned Files: `middleware/singleflight/response_regression_test.go`
Depends On: 无

Goal: 合流重放保留重复响应头
Scope: 为拥有者写入两个普通 Link 响应头，用通道保证另一个请求已加入。
Non-goals: 范围限于本卡涉及的契约，保持公开签名和依赖。
Files: `middleware/singleflight/response_regression_test.go`。
Tests: 拥有者和等待者的 Link 值及顺序相同，状态与正文相等。
Docs Sync: 核对新增验收与已有模块说明一致，记录证据和限制。
Done Definition: 完成以下验收，记录命令、结果与剩余限制。
Outcome: 用旧合流同步夹具验证两个同名 Link 值的顺序在原请求及两个等待者中一致。

## 证据与影响

证据状态为补证。第 9 节检查号为 6,23,24,34,39。 [质量规则第 9 节](../../../docs/operations/agent-code-quality-rules.md#L267)

已缓冲响应为等待者重放时须保留同名多个值。 [middleware/singleflight/singleflight.go:376](../../../middleware/singleflight/singleflight.go#L376)

[复审报告](../../../docs/evidence/stable-quality-review-c3a77410.md)

## 验收与验证

- [x] 拥有者和等待者的 Link 值及顺序相同，状态与正文相等。
- [x] 已有测试覆盖相同触发和断言时可直接复用，记录具体用例。

```sh
go test -race -timeout 60s ./middleware/singleflight
```

## 实施证据

- [TestCoalescedResponsesApplyHeaderChangesToEachWaiter](../../../middleware/singleflight/response_regression_test.go#L320)
