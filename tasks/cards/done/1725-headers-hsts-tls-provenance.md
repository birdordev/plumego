# Card 1725 — 核对直接 TLS 与可信代理约定

Milestone: 稳定模块质量复审
Recipe: specs/change-recipes/add-acceptance-tests.yaml
Context Package: implementation
Priority: P2
State: done
Primary Module: security
Owned Files: `security/headers/headers_test.go`, `docs/modules/security/README.md`
Depends On: 无

Goal:

核对直接 TLS 与可信代理约定。

Scope:

在合法本地 TLS 和无 TLS 测试对象验证 HSTS，并核对库边界说明。

Non-goals:

本卡范围限于所列文件。维持公开签名和现有依赖，兼容契约调整先核对调用边界。

Files:

- `docs/modules/security/README.md`
- `security/headers/headers_test.go`

## Impact and Ownership

主模块为 `security`。根模块继续使用标准库。候审目标是复核可观察契约并记录证据。

## 证据

检查号为 14,18,40,46，状态为补证。

默认 HSTS 条件依赖请求的 TLS 语义，代理部署需明确由宿主传递可信来源。 [security/headers/headers.go:415](https://github.com/spcent/plumego/blob/9430574179dc6b793524992833ce27496a5fe548/security/headers/headers.go#L415)

[复审报告](../../../docs/evidence/stable-quality-review-94305741.md)

## Acceptance Tests

- `security/headers/headers_test.go` 中的 `TestStrictPolicyHSTS`
- `security/headers/headers_test.go` 中的 `TestStrictPolicyHSTSWithForwardedProtocols`
- `security/headers/headers_test.go` 中的 `TestStrictPolicyDoesNotTrustForwardedSSLForHSTS`

正确的直接 TLS 条件生成 HSTS，代理策略由所属应用负责。

Tests:

通过所列模块命令运行相邻用例并核对现有约定。

Docs Sync:

已同步 security 模块说明，明确转发头的可信来源。

## Validation

```sh
go test -race -timeout 60s ./security/headers
make website-sync
make website-check-generated
```

Done Definition:

- [x] 正确的直接 TLS 条件生成 HSTS，代理策略由所属应用负责。
- [x] 执行验证命令，记录状态、首次失败和剩余核验范围。

Outcome:

复用直连 TLS 与转发协议矩阵。模块说明明确可信入口须去除不可信转发头并发布自己的协议链。

## 实施证据

- [TestStrictPolicyHSTS](../../../security/headers/headers_test.go#L47)
- [TestStrictPolicyHSTSWithForwardedProtocols](../../../security/headers/headers_test.go#L69)
- [TestStrictPolicyDoesNotTrustForwardedSSLForHSTS](../../../security/headers/headers_test.go#L105)

[批次验证与基准](../review-94305741/README.md)
