# Card 1875 — 核对并发限制在下游异常退出时交还占位

Milestone: 稳定模块质量复审
Recipe: specs/change-recipes/add-acceptance-tests.yaml
Context Package: implementation
Priority: P2
State: done
Primary Module: middleware
Owned Files: `middleware/concurrencylimit/concurrency_limit_test.go`
Depends On: 无

Goal: 两个容量均回收，没有双重拒绝，后续正常调用仍可通过。
Scope: 固定容量 1 并排队一项下游，首 handler 受控退出，检查第二项和随后一项。
Non-goals: 维持稳定公开签名与标准库依赖，将验证限于本卡范围。
Files: `middleware/concurrencylimit/concurrency_limit_test.go`。
Tests: 两个容量均回收，没有双重拒绝，后续正常调用仍可通过。
Docs Sync: 记录核验结论；只在现有契约文字需要澄清时另提文档变更。
Done Definition: 完成下列验收，记录验证命令、状态及剩余限制。
Outcome: 工作 handler 退出时归还 worker 和 queue 占位，排队和随后请求均可进入。

## 证据与影响

第 9 节检查号为 20,26,28,29,39。证据状态为补证。

worker 和 queue 两个位置分别由 defer 归还。需验证有请求排队时 Goexit 后的移交与新周期准入。 [middleware/concurrencylimit/concurrency_limit.go:95](../../../middleware/concurrencylimit/concurrency_limit.go#L95)

[复审报告](../../../docs/evidence/stable-quality-review-a8c2f28a.md)

## 验收与验证

- [x] 两个容量均回收，没有双重拒绝，后续正常调用仍可通过。
- [x] 记录挂载入口、具体断言和结果。已有测试能覆盖时复用并注明。

```sh
go test -race -timeout 60s ./middleware/concurrencylimit
```

## 实施证据

- [TestAbnormalWorkerExitReleasesBothAdmissionSlots](../../../middleware/concurrencylimit/concurrency_limit_test.go#L463)

[批次验证与基准](../review-a8c2f28a/README.md)
