# Card 1871 — 核对等待者超时与独立取消的归属

Milestone: 稳定模块质量复审
Recipe: specs/change-recipes/add-acceptance-tests.yaml
Context Package: implementation
Priority: P2
State: done
Primary Module: middleware
Owned Files: `middleware/singleflight/singleflight_test.go`
Depends On: 无

Goal: 取消的请求无迟到重放，正常等待者按原先完成顺序取得结果。
Scope: 用同一合成 key 对应普通内容，交错三个请求并控制完成时序，观察 OnError 和 leader 计数。
Non-goals: 维持稳定公开签名与标准库依赖，将验证限于本卡范围。
Files: `middleware/singleflight/singleflight_test.go`。
Tests: 取消的请求无迟到重放，正常等待者按原先完成顺序取得结果。
Docs Sync: 记录核验结论；只在现有契约文字需要澄清时另提文档变更。
Done Definition: 完成下列验收，记录验证命令、状态及剩余限制。
Outcome: 一个等待者取消后没有重放或错误回调，同批 leader 与另一个等待者仍取得同一普通响应。复用既有超时用例。

## 证据与影响

第 9 节检查号为 26,28,29,39,47。证据状态为补证。

结束时间和 done 表示 leader 完成，等待者还有各自 Context。兄弟请求超时不应影响 leader 和另一等待者。 [middleware/singleflight/singleflight.go:144](../../../middleware/singleflight/singleflight.go#L144)

[复审报告](../../../docs/evidence/stable-quality-review-a8c2f28a.md)

## 验收与验证

- [x] 取消的请求无迟到重放，正常等待者按原先完成顺序取得结果。
- [x] 记录挂载入口、具体断言和结果。已有测试能覆盖时复用并注明。

```sh
go test -race -timeout 60s ./middleware/singleflight
```

## 实施证据

- [TestSiblingWaiterCancelDoesNotInterruptLeaderAndReplay](../../../middleware/singleflight/singleflight_test.go#L1619)
- [TestCoalesce_WaiterTimeoutDoesNotLeakInFlightRequest](../../../middleware/singleflight/singleflight_test.go#L839)

[批次验证与基准](../review-a8c2f28a/README.md)
