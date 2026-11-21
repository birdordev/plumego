# Card 1977 — 核对下游显式编码的流不会叠加 gzip

Milestone: 稳定模块质量复审
Recipe: specs/change-recipes/add-acceptance-tests.yaml
Context Package: implementation
Priority: P2
State: done
Primary Module: middleware/compression
Owned Files: `middleware/compression/gzip_test.go`
Depends On: 无

Goal: 最终只有下游选定的编码，状态仅提交一次，字节顺序不变。
Scope: 在本地写入带现有 Content-Encoding 的合成内容并 Flush，再继续写。
Non-goals: 维持稳定公开签名与标准库依赖，将验证限于本卡范围。
Files: `middleware/compression/gzip_test.go`。
Tests: 最终只有下游选定的编码，状态仅提交一次，字节顺序不变。
Docs Sync: 记录核验结论；只在现有契约文字需要澄清时另提文档变更。
Done Definition: 完成下列验收，记录验证命令、状态及剩余限制。
Outcome: Flush 前显式编码保留原 Content-Encoding、HTTP 状态和分段内容顺序。

## 证据与影响

第 9 节检查号为 17,23,24,39，当前证据状态为补证。 [质量规则第 9 节](../../../docs/operations/agent-code-quality-rules.md#L267)

真实 Flush 可在 wrapper 仍持有状态时触发写入。下游已声明编码时需核对过渡的普通流。 [middleware/compression/gzip.go:473](../../../middleware/compression/gzip.go#L473)

[复审报告](../../../docs/evidence/stable-quality-review-999d5e4d.md)

## 验收与验证

- [x] 最终只有下游选定的编码，状态仅提交一次，字节顺序不变。
- [x] 记录挂载入口、具体断言和结果。已有测试能覆盖时复用并注明。

```sh
go test -race -timeout 60s ./middleware/compression
```

## 实施证据

- [TestGzipPreservesExplicitEncodingAcrossFlush](../../../middleware/compression/gzip_test.go#L1964)
