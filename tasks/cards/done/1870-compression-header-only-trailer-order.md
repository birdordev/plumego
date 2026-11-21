# Card 1870 — 补证压缩包装空正文时的 Trailer 交付顺序

Milestone: 稳定模块质量复审
Recipe: specs/change-recipes/add-acceptance-tests.yaml
Context Package: implementation
Priority: P2
State: done
Primary Module: middleware
Owned Files: `middleware/compression/gzip_test.go`
Depends On: 无

Goal: 状态不重复提交，Trailer 不成为普通头，空响应无 gzip 数据帧。
Scope: 写入声明的 trailer 后完成空正文，分别用 gzip 与 identity 收集普通 Header 和最终 Trailer。
Non-goals: 维持稳定公开签名与标准库依赖，将验证限于本卡范围。
Files: `middleware/compression/gzip_test.go`。
Tests: 状态不重复提交，Trailer 不成为普通头，空响应无 gzip 数据帧。
Docs Sync: 记录核验结论；只在现有契约文字需要澄清时另提文档变更。
Done Definition: 完成下列验收，记录验证命令、状态及剩余限制。
Outcome: 真实连接上空正文的 gzip 与 identity 均保持尾字段，只提交既定状态且不会生成 gzip 帧。

## 证据与影响

第 9 节检查号为 23,24,39。证据状态为补证。

结束路径延后复制 trailer，空正文仍可能有明确状态和尾字段。需组合无压缩主体与实际 loopback。 [middleware/compression/gzip.go:188](../../../middleware/compression/gzip.go#L188)

[复审报告](../../../docs/evidence/stable-quality-review-a8c2f28a.md)

## 验收与验证

- [x] 状态不重复提交，Trailer 不成为普通头，空响应无 gzip 数据帧。
- [x] 记录挂载入口、具体断言和结果。已有测试能覆盖时复用并注明。

```sh
go test -race -timeout 60s ./middleware/compression
```

## 实施证据

- [TestGzipHeaderOnlyCompletionPreservesTrailer](../../../middleware/compression/gzip_test.go#L1875)

[批次验证与基准](../review-a8c2f28a/README.md)
