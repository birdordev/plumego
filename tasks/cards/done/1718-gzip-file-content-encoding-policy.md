# Card 1718 — 核对 gzip 对媒体编码和摘要头的取舍

Milestone: 稳定模块质量复审
Recipe: specs/change-recipes/add-acceptance-tests.yaml
Context Package: implementation
Priority: P2
State: done
Primary Module: middleware
Owned Files: `middleware/compression/gzip_test.go`
Depends On: 无

Goal:

核对 gzip 对媒体编码和摘要头的取舍。

Scope:

复用现有 gzip 表驱动用例，组合应用显式 Content-Encoding、弱校验和自定义媒体类型。

Non-goals:

本卡范围限于所列文件。维持公开签名和现有依赖，兼容契约调整先核对调用边界。

Files:

- `middleware/compression/gzip_test.go`

## Impact and Ownership

主模块为 `middleware`。根模块继续使用标准库。候审目标是复核可观察契约并记录证据。

## 证据

检查号为 6,23,24,39，状态为补证。

预编码响应、二进制和内容范围绕过压缩，真正变体时清理摘要头。 [middleware/compression/gzip.go:414](https://github.com/spcent/plumego/blob/9430574179dc6b793524992833ce27496a5fe548/middleware/compression/gzip.go#L414)

[复审报告](../../../docs/evidence/stable-quality-review-94305741.md)

## Acceptance Tests

- `middleware/compression/gzip_test.go` 中的 `TestGzipRetainsPreencodedRepresentationMetadata`
- `middleware/compression/gzip_test.go` 中的 `TestGzipPreservesRangeResponses`

直接和压缩两种结果的编码、正文、Vary 和摘要符合计划。

Tests:

通过所列模块命令运行相邻用例并核对现有约定。

Docs Sync:

对照原契约核验，若证据要求修订公开行为，先更新实施范围。

## Validation

```sh
go test -race -timeout 60s ./middleware/compression
```

Done Definition:

- [x] 直接和压缩两种结果的编码、正文、Vary 和摘要符合计划。
- [x] 执行验证命令，记录状态、首次失败和剩余核验范围。

Outcome:

预编码响应保留其编码、正文和摘要头，真实 gzip 输出移除旧摘要且可解码，Vary 保持。

## 实施证据

- [TestGzipRetainsPreencodedRepresentationMetadata](../../../middleware/compression/gzip_test.go#L1736)
- [TestGzipPreservesRangeResponses](../../../middleware/compression/gzip_test.go#L1538)

[批次验证与基准](../review-94305741/README.md)
