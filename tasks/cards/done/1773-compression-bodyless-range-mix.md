# Card 1773 — 核对压缩在 304 和 Range 交叠时的变体头

Milestone: 稳定模块质量复审
Recipe: specs/change-recipes/add-acceptance-tests.yaml
Context Package: implementation
Priority: P2
State: done
Primary Module: middleware
Owned Files: `middleware/compression/gzip_test.go`
Depends On: 无

Goal: 304 无正文，正常变体头与部分内容的原表示一致。
Scope: 以本机静态内容验证普通 GET、匹配 ETag、Range 与 HEAD。
Non-goals: 维持稳定公开签名与标准库依赖，将验证限于本卡范围。
Files: `middleware/compression/gzip_test.go`。
Tests: 304 无正文，正常变体头与部分内容的原表示一致。
Docs Sync: 复核本卡涉及的已公开约定。
Done Definition: 完成下列验收，记录验证命令、状态及剩余限制。
Outcome: 同一 ServeContent 夹具中核对 gzip、304、Range 206 和 HEAD，条件响应保留 Vary 与正确正文边界。

## 证据与影响

第 9 节检查号为 21,23,24,39。证据状态为补证。

Range 与 bodyless 已分开覆盖，需要同一真实 handler 的条件请求对照。 [middleware/compression/gzip.go:414](../../../middleware/compression/gzip.go#L414)

[复审报告](../../../docs/evidence/stable-quality-review-d224acbc.md)

## 验收与验证

- [x] 304 无正文，正常变体头与部分内容的原表示一致。
- [x] 记录运行结果。若已有用例覆盖目标，记录用例及相关断言。

```sh
go test -race -timeout 60s ./middleware/compression
```

## 实施证据

- [TestGzipConditionalAndRangeVariantsKeepFraming](../../../middleware/compression/gzip_test.go#L1775)

[批次验证与基准](../review-d224acbc/README.md)
