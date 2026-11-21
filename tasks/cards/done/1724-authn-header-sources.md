# Card 1724 — 核对静态认证凭据来源说明

Milestone: 稳定模块质量复审
Recipe: specs/change-recipes/add-acceptance-tests.yaml
Context Package: implementation
Priority: P2
State: done
Primary Module: security
Owned Files: `security/authn/token_test.go`
Depends On: 无

Goal:

核对静态认证凭据来源说明。

Scope:

使用非秘密占位值复跑已有码拒绝和缺失路径，限定防御性审查。

Non-goals:

本卡范围限于所列文件。维持公开签名和现有依赖，兼容契约调整先核对调用边界。

Files:

- `security/authn/token_test.go`

## Impact and Ownership

主模块为 `security`。根模块继续使用标准库。候审目标是复核可观察契约并记录证据。

## 证据

检查号为 18,25,40,46，状态为补证。

认证入口按公开请求头规则提取许可格式。需要核对已有文档和示例，与自定义中间件调用方向一致。 [security/authn/token.go:58](https://github.com/spcent/plumego/blob/9430574179dc6b793524992833ce27496a5fe548/security/authn/token.go#L58)

[复审报告](../../../docs/evidence/stable-quality-review-94305741.md)

## Acceptance Tests

- `security/authn/token_test.go` 中的 `TestStaticToken`
- `security/authn/token_test.go` 中的 `TestExtractBearerTokenStrictScheme`
- `middleware/auth/auth_test.go` 中的 `TestAuthenticateMiddlewareInvalidToken`

凭据无效、不存在或无法验证时均不调用受保护目标，响应不回显输入。

Tests:

通过所列模块命令运行相邻用例并核对现有约定。

Docs Sync:

对照原契约核验，若证据要求修订公开行为，先更新实施范围。

## Validation

```sh
go test -race -timeout 60s ./security/authn
```

Done Definition:

- [x] 凭据无效、不存在或无法验证时均不调用受保护目标，响应不回显输入。
- [x] 执行验证命令，记录状态、首次失败和剩余核验范围。

Outcome:

复查静态头凭据与 query 忽略规则。公开认证失败保留统一分类，输出不回显人工凭据。复用 HTTP 中间件的拒绝断言。

## 实施证据

- [TestStaticToken](../../../security/authn/token_test.go#L11)
- [TestExtractBearerTokenStrictScheme](../../../security/authn/token_test.go#L109)
- [TestAuthenticateMiddlewareInvalidToken](../../../middleware/auth/auth_test.go#L89)

[批次验证与基准](../review-94305741/README.md)
