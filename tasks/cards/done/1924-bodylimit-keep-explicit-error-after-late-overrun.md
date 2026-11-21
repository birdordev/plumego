# Card 1924 — 核对已选错误状态后读取超限时的正文归属

Milestone: 稳定模块质量复审
Recipe: specs/change-recipes/add-acceptance-tests.yaml
Context Package: implementation
Priority: P2
State: done
Primary Module: middleware
Owned Files: `middleware/bodylimit/body_limit_test.go`
Depends On: 无

Goal: 前者保留完整业务错误，后者只产生一次传输拒绝，二者都无第二状态。
Scope: 同一短体矩阵让下游先提交业务错误再读和先读至超限再尝试响应，记录状态序列和日志次数。
Non-goals: 维持稳定公开签名与标准库依赖，将验证限于本卡范围。
Files: `middleware/bodylimit/body_limit_test.go`。
Tests: 前者保留完整业务错误，后者只产生一次传输拒绝，二者都无第二状态。
Docs Sync: 记录核验结论；只在现有契约文字需要澄清时另提文档变更。
Done Definition: 完成下列验收，记录验证命令、状态及剩余限制。
Outcome: 下游先选择业务错误后读超限仍保留两次正文写入；先超限时归到传输 413。

## 证据与影响

第 9 节检查号为 20,23,24,32,39。证据状态为补证。

已经提交 4xx 后发现超限，guard 分支保留业务失败正文。需与先读后写的拒绝路径比较。 [middleware/bodylimit/body_limit.go:148](../../../middleware/bodylimit/body_limit.go#L148)

[复审报告](../../../docs/evidence/stable-quality-review-2b94cf86.md)

## 验收与验证

- [x] 前者保留完整业务错误，后者只产生一次传输拒绝，二者都无第二状态。
- [x] 记录挂载入口、具体断言和结果。已有测试能覆盖时复用并注明。

```sh
go test -race -timeout 60s ./middleware/bodylimit
```

## 实施证据

- [TestBodyLimitAfterExplicitErrorKeepsBothBodyWrites](../../../middleware/bodylimit/body_limit_test.go#L852)

[批次验证与基准](../review-2b94cf86/README.md)
