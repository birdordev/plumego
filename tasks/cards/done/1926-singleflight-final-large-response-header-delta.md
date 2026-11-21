# Card 1926 — 核对 singleflight 达到捕获上限时 leader 与 waiter 的头部归属

Milestone: 稳定模块质量复审
Recipe: specs/change-recipes/add-acceptance-tests.yaml
Context Package: implementation
Priority: P2
State: done
Primary Module: middleware
Owned Files: `middleware/singleflight/response_regression_test.go`
Depends On: 无

Goal: leader 完整发送，waiter 不收到残留头体或第二状态，最后下一请求可独立执行。
Scope: 以相同普通 key 受控并发，让 leader 写入两段跨越上限的普通内容，读取双方响应与事件计数。
Non-goals: 维持稳定公开签名与标准库依赖，将验证限于本卡范围。
Files: `middleware/singleflight/response_regression_test.go`。
Tests: leader 完整发送，waiter 不收到残留头体或第二状态，最后下一请求可独立执行。
Docs Sync: 记录核验结论；只在现有契约文字需要澄清时另提文档变更。
Done Definition: 完成下列验收，记录验证命令、状态及剩余限制。
Outcome: leader 超限仍完成完整成功响应；waiter 收到独立固定错误，不复制 leader 的 ETag 或正文，随后请求可独立执行。

## 证据与影响

第 9 节检查号为 20,23,24,35,39。证据状态为补证。

捕获溢出不会限制 leader 输出，waiter 使用固定错误。需要联合核对预置的业务 Header 和不可回放的表示字段。 [middleware/singleflight/singleflight.go:537](../../../middleware/singleflight/singleflight.go#L537)

[复审报告](../../../docs/evidence/stable-quality-review-2b94cf86.md)

## 验收与验证

- [x] leader 完整发送，waiter 不收到残留头体或第二状态，最后下一请求可独立执行。
- [x] 记录挂载入口、具体断言和结果。已有测试能覆盖时复用并注明。

```sh
go test -race -timeout 60s ./middleware/singleflight
```

## 实施证据

- [TestOverflowingLeaderRetainsHeadersButWaiterGetsOwnError](../../../middleware/singleflight/response_regression_test.go#L797)

[批次验证与基准](../review-2b94cf86/README.md)
