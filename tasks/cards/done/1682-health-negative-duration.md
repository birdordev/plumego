# Card 1682 — 明确模型 Duration 的负值线格式

Milestone: 稳定模块质量复审
Recipe: specs/change-recipes/add-acceptance-tests.yaml
Context Package: implementation
Priority: P3
State: done
Primary Module: health
Owned Files: `health/json_contract_test.go`
Depends On: 无

Goal: 明确模型 Duration 的负值线格式。
Scope: 用固定零、正、负值核对线格式和文档。
Non-goals: 维持公开签名和依赖；涉及现有兼容契约的调整须先核对调用边界及迁移影响。
Files: `health/json_contract_test.go`。
Tests: 模型保留既有输入类型，没有改动求时策略。
Docs Sync: 对照原契约核验，若证据要求修订公开行为，先更新实施范围。
Done Definition: 完成测试与验收记录，并标明剩余取证范围。
Outcome: 健康时长线格式核对为带符号纳秒，零值省略。

## 证据

检查号为 6,16,42，状态为补证。

时间字段以纳秒序列化且零会省略，负值是否允许由模型调用者约定。 [health/core.go:33](../../../health/core.go#L33)

[复审报告](../../../docs/evidence/stable-quality-review-09a7f5d3.md)

## 验收与验证

- [x] 模型保留既有输入类型，没有改动求时策略。
- [x] 记录验证命令、状态和剩余范围。若使用既有用例满足验收，记录对应证据。

```sh
go test -race -timeout 60s ./health
```

## 实施证据

[TestHealthDurationUsesSignedNanoseconds](../../../health/json_contract_test.go#L203)
