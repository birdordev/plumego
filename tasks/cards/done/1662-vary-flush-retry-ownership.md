# Card 1662 — 验证 Vary writer 在不支持 Flush 后的状态回滚

Milestone: 稳定模块质量复审
Recipe: specs/change-recipes/add-acceptance-tests.yaml
Context Package: implementation
Priority: P2
State: done
Primary Module: middleware
Owned Files: `middleware/conformance/response_writer_contract_test.go`
Depends On: 无

Goal: 验证 Vary writer 在不支持 Flush 后的状态回滚。
Scope: 在内外链上模拟首次拒绝，然后替换 Vary 并完成输出。
Non-goals: 维持公开签名和依赖；涉及现有兼容契约的调整须先核对调用边界及迁移影响。
Files: `middleware/conformance/response_writer_contract_test.go`。
Tests: 最终声明保留所需字段且无重复，原处理器仍可选最终状态。
Docs Sync: 对照原契约核验，若证据要求修订公开行为，先更新实施范围。
Done Definition: 完成测试与验收记录，并标明剩余取证范围。
Outcome: 组合 writer 拒绝 Flush 后允许应用替换 Vary 并选择最终状态，重新合并字段且无重复。

## 证据

检查号为 23,24,34,39，状态为补证。

VaryWriter 在 Flush 前改 Header，ErrNotSupported 时仅恢复 committed 标志。 [internal/httputil/vary_writer.go:69](../../../internal/httputil/vary_writer.go#L69)

[复审报告](../../../docs/evidence/stable-quality-review-09a7f5d3.md)

## 验收与验证

- [x] 最终声明保留所需字段且无重复，原处理器仍可选最终状态。
- [x] 记录验证命令、状态和剩余范围。若使用既有用例满足验收，记录对应证据。

```sh
go test -race -timeout 60s ./middleware/conformance
```

## 实施证据

[TestVaryFallbackAfterUnsupportedFlush](../../../middleware/conformance/response_writer_contract_test.go#L455)
