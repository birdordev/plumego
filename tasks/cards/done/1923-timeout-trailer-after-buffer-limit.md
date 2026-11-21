# Card 1923 — 核对超时回放容量失败后旧 Trailer 的出口归属

Milestone: 稳定模块质量复审
Recipe: specs/change-recipes/add-acceptance-tests.yaml
Context Package: implementation
Priority: P2
State: done
Primary Module: middleware
Owned Files: `middleware/timeout/timeout_regression_test.go`
Depends On: 无

Goal: 替代响应只有一个终态，旧表示尾字段与正文均不再输出。
Scope: 设置正常元数据和合成尾字段，使回放体越过 MaxReplayBytes，检查替代状态、Header 与 Trailer。
Non-goals: 维持稳定公开签名与标准库依赖，将验证限于本卡范围。
Files: `middleware/timeout/timeout_regression_test.go`。
Tests: 替代响应只有一个终态，旧表示尾字段与正文均不再输出。
Docs Sync: 记录核验结论；只在现有契约文字需要澄清时另提文档变更。
Done Definition: 完成下列验收，记录验证命令、状态及剩余限制。
Outcome: 响应超过回放上限时只提交一份替代响应，缓冲正文、尾字段和内部头部不进入替代响应。

## 证据与影响

第 9 节检查号为 23,24,35,39,46。证据状态为补证。

回放错误会重写表示字段。已声明 Trailer 和迟到 header 在容量被丢弃时是否跨到替代响应，需组合回归。 [middleware/timeout/timeout.go:279](../../../middleware/timeout/timeout.go#L279)

[复审报告](../../../docs/evidence/stable-quality-review-2b94cf86.md)

## 验收与验证

- [x] 替代响应只有一个终态，旧表示尾字段与正文均不再输出。
- [x] 记录挂载入口、具体断言和结果。已有测试能覆盖时复用并注明。

```sh
go test -race -timeout 60s ./middleware/timeout
```

## 实施证据

- [TestReplayLimitDiscardsBufferedTrailerAndBody](../../../middleware/timeout/timeout_regression_test.go#L523)

[批次验证与基准](../review-2b94cf86/README.md)
