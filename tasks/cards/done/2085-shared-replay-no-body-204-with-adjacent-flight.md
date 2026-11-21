# Card 2085 — 合流 204 的空体重放与下一批隔离

Milestone: 稳定模块第 9 节复审
Recipe: specs/change-recipes/add-acceptance-tests.yaml
Context Package: implementation
Priority: P2
State: done
Primary Module: middleware/singleflight
Owned Files: `middleware/singleflight/response_regression_test.go`
Depends On: 无

Goal: 合流 204 的空体重放与下一批隔离
Scope: 首批用通道固定 owner 与 waiter，均接收 204。释放后同 key 发第二批。
Non-goals: 范围限于本卡涉及的契约，保持公开签名和依赖。
Files: `middleware/singleflight/response_regression_test.go`。
Tests: 首批状态及普通头保持，第二批重新执行且不携带首批头。
Docs Sync: 核对新增验收与已有模块说明一致，记录证据和限制。
Done Definition: 完成以下验收，记录命令、结果与剩余限制。
Outcome: 持有者和等待者均收到同一 204，下一次同 key 重新进入下游且没有上一批头。

## 证据与影响

证据状态为补证。第 9 节检查号为 23,24,31,39,47。 [质量规则第 9 节](../../../docs/operations/agent-code-quality-rules.md#L267)

成功的无正文响应可被多个等待者重放，下一批应拥有新响应。 [middleware/singleflight/singleflight.go:376](../../../middleware/singleflight/singleflight.go#L376)

[复审报告](../../../docs/evidence/stable-quality-review-2f962d21.md)

## 验收与验证

- [x] 首批状态及普通头保持，第二批重新执行且不携带首批头。
- [x] 已有测试覆盖相同触发和断言时可直接复用，记录具体用例。

```sh
go test -race -timeout 60s ./middleware/singleflight
```

## 实施证据

- [TestCoalescedNoContentDoesNotLeakIntoNextFlight](../../../middleware/singleflight/response_regression_test.go#L878)
