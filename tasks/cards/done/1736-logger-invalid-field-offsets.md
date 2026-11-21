# Card 1736 — 测量字段拒绝与多层截断的边界

Milestone: 稳定模块质量复审
Recipe: specs/change-recipes/add-acceptance-tests.yaml
Context Package: implementation
Priority: P2
State: done
Primary Module: log
Owned Files: `log/json_test.go`
Depends On: 无

Goal:

测量字段拒绝与多层截断的边界。

Scope:

用受控普通值组合无效键和宽度边界，并对照情况。

Non-goals:

本卡范围限于所列文件。维持公开签名和现有依赖，兼容契约调整先核对调用边界。

Files:

- `log/json_test.go`

## Impact and Ownership

主模块为 `log`。根模块继续使用标准库。候审目标是复核可观察契约并记录证据。

## 证据

检查号为 25,35,43,48，状态为补证。

节点限制与不合法 UTF-8 名称处理各自有单元测试；需核对混合输入时安全字段保留和格式。 [log/json.go:366](https://github.com/spcent/plumego/blob/9430574179dc6b793524992833ce27496a5fe548/log/json.go#L366)

[复审报告](../../../docs/evidence/stable-quality-review-94305741.md)

## Acceptance Tests

- `log/json_test.go` 中的 `TestMixedInvalidFieldsAndTruncationKeepSiblingPrecision`
- `log/json_test.go` 中的 `BenchmarkNormalizeJSONFieldMapLimits`

整行 JSON 可解析，缺省截断标记、精确整数和安全字段仍符合契约。

Tests:

通过所列模块命令运行相邻用例并核对现有约定。

Docs Sync:

对照原契约核验，若证据要求修订公开行为，先更新实施范围。

## Validation

```sh
go test -race -timeout 60s ./log
go test ./log -run '^$' -bench '^BenchmarkNormalizeJSONFieldMapLimits$' -benchmem -benchtime=200ms -count=3
```

Done Definition:

- [x] 整行 JSON 可解析，缺省截断标记、精确整数和安全字段仍符合契约。
- [x] 执行验证命令，记录状态、首次失败和剩余核验范围。

Outcome:

验证无效字段名与嵌套截断并存时，兄弟字段的高位整数精度及固定截断标记保留。记录字段归一化基准。

## 实施证据

- [TestMixedInvalidFieldsAndTruncationKeepSiblingPrecision](../../../log/json_test.go#L865)
- [BenchmarkNormalizeJSONFieldMapLimits](../../../log/json_test.go#L886)

[批次验证与基准](../review-94305741/README.md)
