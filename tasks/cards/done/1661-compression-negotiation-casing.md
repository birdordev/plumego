# Card 1661 — 核对 SSE 媒体类型解析与权重契约

Milestone: 稳定模块质量复审
Recipe: specs/change-recipes/add-acceptance-tests.yaml
Context Package: implementation
Priority: P2
State: done
Primary Module: middleware
Owned Files: `middleware/compression/gzip_test.go`, `docs/modules/middleware/README.md`
Depends On: 无

Goal: 核对 SSE 媒体类型解析与权重契约。
Scope: 在现有 Vary 用例记录三个变体的行为，并核对实现支持的解析范围。
Non-goals: 维持公开签名和依赖；涉及现有兼容契约的调整须先核对调用边界及迁移影响。
Files: `middleware/compression/gzip_test.go`, `docs/modules/middleware/README.md`。
Tests: 多值和普通编码变体具备确定输出，升级连接仍可用。
Docs Sync: 对照原契约核验，若证据要求修订公开行为，先更新实施范围。
Done Definition: 完成测试与验收记录，并标明剩余取证范围。
Outcome: 明确当前 SSE 请求绕过使用首个 Accept 字面标记，回归记录大小写、重复字段、显式权重和 Vary 行为。

## 证据

检查号为 6,18,24,39，状态为补证。

SSE 分支使用首个 Accept 值和字符串包含，gzip Accept-Encoding 则组合字段。需明确重复字段、大小写和 q 参数预期。 [middleware/compression/gzip.go:73](../../../middleware/compression/gzip.go#L73)

[复审报告](../../../docs/evidence/stable-quality-review-09a7f5d3.md)

## 验收与验证

- [x] 多值和普通编码变体具备确定输出，升级连接仍可用。
- [x] 记录验证命令、状态和剩余范围。若使用既有用例满足验收，记录对应证据。

```sh
go test -race -timeout 60s ./middleware/compression
```

## 实施证据

[TestSSERequestOptOutAndVaryAcrossAcceptValues](../../../middleware/compression/gzip_test.go#L1707) · [TestVaryCoversEventStreamBypass](../../../middleware/compression/gzip_test.go#L1640)
