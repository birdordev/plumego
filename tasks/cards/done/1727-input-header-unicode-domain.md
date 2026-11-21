# Card 1727 — 补证 Header 值接受集和诊断输出

Milestone: 稳定模块质量复审
Recipe: specs/change-recipes/add-acceptance-tests.yaml
Context Package: implementation
Priority: P2
State: done
Primary Module: security
Owned Files: `security/input/input_test.go`
Depends On: 无

Goal:

补证 Header 值接受集和诊断输出。

Scope:

对照安全请求字段和默认非法类型，仅用本地非敏感 fixtures。

Non-goals:

本卡范围限于所列文件。维持公开签名和现有依赖，兼容契约调整先核对调用边界。

Files:

- `security/input/input_test.go`

## Impact and Ownership

主模块为 `security`。根模块继续使用标准库。候审目标是复核可观察契约并记录证据。

## 证据

检查号为 18,25,40,46，状态为补证。

通用 header 校验供配置与请求适配，并不定义整个协议解析。 [security/input/input.go:120](https://github.com/spcent/plumego/blob/9430574179dc6b793524992833ce27496a5fe548/security/input/input.go#L120)

[复审报告](../../../docs/evidence/stable-quality-review-94305741.md)

## Acceptance Tests

- `security/input/input_test.go` 中的 `TestHeaderValueUTF8AndControlBoundary`
- `security/input/input_test.go` 中的 `TestIsHeaderValue`

守住 Accept/拒绝边界，保持调用者诊断可分类。

Tests:

通过所列模块命令运行相邻用例并核对现有约定。

Docs Sync:

对照原契约核验，若证据要求修订公开行为，先更新实施范围。

## Validation

```sh
go test -race -timeout 60s ./security/input
```

Done Definition:

- [x] 守住 Accept/拒绝边界，保持调用者诊断可分类。
- [x] 执行验证命令，记录状态、首次失败和剩余核验范围。

Outcome:

验证有效 UTF-8、允许的 HTAB 以及无效字节或控制字符的拒绝约定。

## 实施证据

- [TestHeaderValueUTF8AndControlBoundary](../../../security/input/input_test.go#L526)
- [TestIsHeaderValue](../../../security/input/input_test.go#L30)

[批次验证与基准](../review-94305741/README.md)
