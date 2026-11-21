# Card 1738 — 核对 JSON 自定义字段失败与完整写入组合

Milestone: 稳定模块质量复审
Recipe: specs/change-recipes/add-acceptance-tests.yaml
Context Package: implementation
Priority: P3
State: done
Primary Module: log
Owned Files: `log/json_test.go`
Depends On: 无

Goal:

核对 JSON 自定义字段失败与完整写入组合。

Scope:

组合自定义编码回调和 counting 输出。

Non-goals:

本卡范围限于所列文件。维持公开签名和现有依赖，兼容契约调整先核对调用边界。

Files:

- `log/json_test.go`

## Impact and Ownership

主模块为 `log`。根模块继续使用标准库。候审目标是复核可观察契约并记录证据。

## 证据

检查号为 23,33,43,46，状态为补证。

编码器 panic 恢复和短写降级分别有证据。需要确认坏单字段不会让仍有效的应用记录消失。 [log/json.go:282](https://github.com/spcent/plumego/blob/9430574179dc6b793524992833ce27496a5fe548/log/json.go#L282)

[复审报告](../../../docs/evidence/stable-quality-review-94305741.md)

## Acceptance Tests

- `log/json_test.go` 中的 `TestJSONEncoderPanicIsIsolatedToTheField`

只调用一次输出，日志保留可用值及固定占位符。

Tests:

通过所列模块命令运行相邻用例并核对现有约定。

Docs Sync:

对照原契约核验，若证据要求修订公开行为，先更新实施范围。

## Validation

```sh
go test -race -timeout 60s ./log
```

Done Definition:

- [x] 只调用一次输出，日志保留可用值及固定占位符。
- [x] 执行验证命令，记录状态、首次失败和剩余核验范围。

Outcome:

字段编码器 panic 时保留固定占位、有效字段和一次完整输出。

## 实施证据

- [TestJSONEncoderPanicIsIsolatedToTheField](../../../log/json_test.go#L788)

[批次验证与基准](../review-94305741/README.md)
