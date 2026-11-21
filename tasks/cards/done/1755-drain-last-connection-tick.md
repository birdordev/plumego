# Card 1755 — 核对最后连接关闭与排水 ticker 同时就绪

Milestone: 稳定模块质量复审
Recipe: specs/change-recipes/add-acceptance-tests.yaml
Context Package: implementation
Priority: P2
State: done
Primary Module: core
Owned Files: `core/lifecycle_test.go`
Depends On: 无

Goal: 无持续诊断，槽位可重新取得，任何瞬时记录符合约定。
Scope: 用合成时钟和受控 logger 核对收尾诊断及后续槽位。
Non-goals: 维持稳定公开签名与标准库依赖，将验证限于本卡范围。
Files: `core/lifecycle_test.go`。
Tests: 无持续诊断，槽位可重新取得，任何瞬时记录符合约定。
Docs Sync: 复核本卡涉及的已公开约定。
Done Definition: 完成下列验收，记录验证命令、状态及剩余限制。
Outcome: 冻结第一次排水诊断直至下一 tick 到期，最后连接关闭取消本轮。没有额外诊断，后续排水获得资源。

## 证据与影响

第 9 节检查号为 28,32,36,47。证据状态为补证。

退出条件与 tick 后读取的计数不是一次采样。最后连接关闭的取消路径已有单测，接近 tick 时尚无结果对照。 [core/lifecycle.go:187](../../../core/lifecycle.go#L187)

[复审报告](../../../docs/evidence/stable-quality-review-d224acbc.md)

## 验收与验证

- [x] 无持续诊断，槽位可重新取得，任何瞬时记录符合约定。
- [x] 记录运行结果。若已有用例覆盖目标，记录用例及相关断言。

```sh
go test -race -timeout 60s ./core
```

## 实施证据

- [TestDrainLastCloseStopsBeforeNextDiagnostic](../../../core/lifecycle_test.go#L1278)

[批次验证与基准](../review-d224acbc/README.md)
