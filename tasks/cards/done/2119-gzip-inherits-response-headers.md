# Card 2119 — 保留 gzip 外层响应头和既有编码

Milestone: 稳定模块第 9 节复审
Recipe: specs/change-recipes/fix-bug.yaml
Context Package: implementation
Priority: P1
State: done
Primary Module: middleware/compression
Owned Files: `middleware/compression/gzip.go`, `middleware/compression/gzip_test.go`, `middleware/compression/informational_regression_test.go`
Depends On: 无

Goal: 保留 gzip 外层响应头和既有编码。
Scope: 从外层头初始化本地快照，并在同一终态提交路径同步下游对继承字段的新增、替换和删除。保持 Vary、临时 1xx 和 trailer 的所有权。
Non-goals: 保持公开签名、依赖和其余模块的行为。
Files: `middleware/compression/gzip.go`, `middleware/compression/gzip_test.go`, `middleware/compression/informational_regression_test.go`
Tests: 先用外层预设编码和合成正文锁定字节一致性，再覆盖删除继承字段、不同层追加 Vary 及内外层的 trailer。
Docs Sync: 核对压缩公开注释与模块说明。公开说明所列行为有变化时，在实现卡中同步。
Done Definition: 完成以下回归和验收，记录实际命令及结果。
Outcome: gzip 协商路径继承外层响应头快照。原有编码仍采用原始字节，继承字段删除、追加 Vary 和 trailer 都在同一响应中生效。压缩包定向竞态测试通过。

## 证据与影响

证据状态为已复现。第 9 节检查号为 6,17,23,24,34,39,45。启用 gzip 时，包装器从空私有 Header 开始。外层已声明 gzip 并交给下游写入预编码字节时，下游读不到外层头，输出经过第二次压缩，但对外仍只有一层编码声明。 [middleware/compression/gzip.go:453](../../../middleware/compression/gzip.go#L453) [middleware/compression/gzip.go:414](../../../middleware/compression/gzip.go#L414) [middleware/compression/gzip_test.go:708](../../../middleware/compression/gzip_test.go#L708)

[复审报告](../../../docs/evidence/stable-quality-review-6e8d53f9.md)

## 验收与验证

- [x] 先用外层预设编码和合成正文锁定字节一致性，再覆盖删除继承字段、不同层追加 Vary 及内外层的 trailer。
- [x] 完成以下回归和验收，记录实际命令及结果。

```sh
go test -race -timeout 60s ./middleware/compression/...
```

实现时先运行 `make fmt`，按实际改动范围预览并运行 diff 门禁。
