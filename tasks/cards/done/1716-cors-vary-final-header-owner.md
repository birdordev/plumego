# Card 1716 — 验证已选 CORS 响应头与下游替换的所有权

Milestone: 稳定模块质量复审
Recipe: specs/change-recipes/add-acceptance-tests.yaml
Context Package: implementation
Priority: P2
State: done
Primary Module: middleware
Owned Files: `middleware/cors/cors_test.go`
Depends On: 无

Goal:

验证已选 CORS 响应头与下游替换的所有权。

Scope:

以受控 handler 返回普通状态和独立 Vary 值，验证协议头合并。

Non-goals:

本卡范围限于所列文件。维持公开签名和现有依赖，兼容契约调整先核对调用边界。

Files:

- `middleware/cors/cors_test.go`

## Impact and Ownership

主模块为 `middleware`。根模块继续使用标准库。候审目标是复核可观察契约并记录证据。

## 证据

检查号为 20,23,24,39，状态为补证。

应用可在允许请求进入下游后自行写 header。需要明确先置 CORS 策略和终态 Vary 组合。 [middleware/cors/cors.go:218](https://github.com/spcent/plumego/blob/9430574179dc6b793524992833ce27496a5fe548/middleware/cors/cors.go#L218)

[复审报告](../../../docs/evidence/stable-quality-review-94305741.md)

## Acceptance Tests

- `middleware/cors/cors_test.go` 中的 `TestCORSAddsRequiredVaryAfterDownstreamReplacement`

预检直终止一次；正常透传不重复字段，最终 Vary 可追溯。

Tests:

通过所列模块命令运行相邻用例并核对现有约定。

Docs Sync:

对照原契约核验，若证据要求修订公开行为，先更新实施范围。

## Validation

```sh
go test -race -timeout 60s ./middleware/cors
```

Done Definition:

- [x] 预检直终止一次；正常透传不重复字段，最终 Vary 可追溯。
- [x] 执行验证命令，记录状态、首次失败和剩余核验范围。

Outcome:

下游替换 Vary 时合并 Origin 与业务字段，Origin 只出现一次，预检仍不调用下游。

## 实施证据

- [TestCORSAddsRequiredVaryAfterDownstreamReplacement](../../../middleware/cors/cors_test.go#L643)

[批次验证与基准](../review-94305741/README.md)
