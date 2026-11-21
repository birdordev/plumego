# Card 1734 — 验证组件启用标志与健康状态的序列化独立性

Milestone: 稳定模块质量复审
Recipe: specs/change-recipes/add-acceptance-tests.yaml
Context Package: implementation
Priority: P2
State: done
Primary Module: health
Owned Files: `health/json_contract_test.go`
Depends On: 无

Goal:

验证组件启用标志与健康状态的序列化独立性。

Scope:

构造禁用且状态非空、启用但状态未知的组件，检查稳定模型的 JSON 和克隆。

Non-goals:

本卡范围限于所列文件。维持公开签名和现有依赖，兼容契约调整先核对调用边界。

Files:

- `health/json_contract_test.go`

## Impact and Ownership

主模块为 `health`。根模块继续使用标准库。候审目标是复核可观察契约并记录证据。

## 证据

检查号为 6,16,38,42，状态为补证。

ComponentHealth 分别携带启用标志和健康状态。调用方可独立设置两个维度，现有空值测试不足以说明混合结果。 [health/core.go:40](https://github.com/spcent/plumego/blob/9430574179dc6b793524992833ce27496a5fe548/health/core.go#L40)

[复审报告](../../../docs/evidence/stable-quality-review-94305741.md)

## Acceptance Tests

- `health/json_contract_test.go` 中的 `TestComponentEnabledAndStatusHaveIndependentWireValues`

两个字段均按传入值输出，克隆不推导或修改任一状态。

Tests:

通过所列模块命令运行相邻用例并核对现有约定。

Docs Sync:

对照原契约核验，若证据要求修订公开行为，先更新实施范围。

## Validation

```sh
go test -race -timeout 60s ./health
```

Done Definition:

- [x] 两个字段均按传入值输出，克隆不推导或修改任一状态。
- [x] 执行验证命令，记录状态、首次失败和剩余核验范围。

Outcome:

启用位和健康状态独立序列化。不同组合经 Clone 后仍保留原传入值。

## 实施证据

- [TestComponentEnabledAndStatusHaveIndependentWireValues](../../../health/json_contract_test.go#L214)

[批次验证与基准](../review-94305741/README.md)
