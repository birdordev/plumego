# Card 2031 — 压缩协商与部分内容的表示一致性

Milestone: 稳定模块第 9 节复审
Recipe: specs/change-recipes/add-acceptance-tests.yaml
Context Package: implementation
Priority: P2
State: done
Primary Module: middleware/compression
Owned Files: `middleware/compression/gzip_test.go`
Depends On: 无

Goal: 压缩协商与部分内容的表示一致性
Scope: 用固定范围内容经支持 gzip 的普通回环请求读取。
Non-goals: 范围限于本卡涉及的契约，保持公开签名和依赖。
Files: `middleware/compression/gzip_test.go`。
Tests: partial 响应保留原范围、长度与原内容，Vary 行为按现有契约。
Docs Sync: 核对新增验收与已有模块说明一致，记录证据和限制。
Done Definition: 完成以下验收，记录命令、结果与剩余限制。
Outcome: 回环验证 206 范围、长度、Vary 与未压缩正文一致。

## 证据与影响

证据状态为补证。第 9 节检查号为 17,23,24,39。 [质量规则第 9 节](../../../docs/operations/agent-code-quality-rules.md#L267)

gzip 根据状态与响应头选择表示。核验实际回环部分内容中的长度、范围和字节。 [middleware/compression/gzip.go:414](../../../middleware/compression/gzip.go#L414)

[复审报告](../../../docs/evidence/stable-quality-review-c3a77410.md)

## 验收与验证

- [x] partial 响应保留原范围、长度与原内容，Vary 行为按现有契约。
- [x] 已有测试覆盖相同触发和断言时可直接复用，记录具体用例。

```sh
go test -race -timeout 60s ./middleware/compression
```

## 实施证据

- [TestGzipRangePreservesFramingOnLoopback](../../../middleware/compression/gzip_test.go#L1983)
