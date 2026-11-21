# Card 1726 — 补证管理器持有配置的复制及默认值

Milestone: 稳定模块质量复审
Recipe: specs/change-recipes/add-acceptance-tests.yaml
Context Package: implementation
Priority: P2
State: done
Primary Module: security
Owned Files: `security/jwt/jwt_test.go`
Depends On: 无

Goal:

补证管理器持有配置的复制及默认值。

Scope:

通过公开有效 fixture 核对调用方构造后改变自己的配置值。

Non-goals:

本卡范围限于所列文件。维持公开签名和现有依赖，兼容契约调整先核对调用边界。

Files:

- `security/jwt/jwt_test.go`

## Impact and Ownership

主模块为 `security`。根模块继续使用标准库。候审目标是复核可观察契约并记录证据。

## 证据

检查号为 5,14,34,40，状态为补证。

JWT 配置由构造归一化，允许值和默认项影响签发与验证。 [security/jwt/config.go:159](https://github.com/spcent/plumego/blob/9430574179dc6b793524992833ce27496a5fe548/security/jwt/config.go#L159)

[复审报告](../../../docs/evidence/stable-quality-review-94305741.md)

## Acceptance Tests

- `security/jwt/jwt_test.go` 中的 `TestJWTManagerKeepsNormalizedConfigSnapshot`

已有实例行为稳定，默认结果和错误仍保持。

Tests:

通过所列模块命令运行相邻用例并核对现有约定。

Docs Sync:

对照原契约核验，若证据要求修订公开行为，先更新实施范围。

## Validation

```sh
go test -race -timeout 60s ./security/jwt
```

Done Definition:

- [x] 已有实例行为稳定，默认结果和错误仍保持。
- [x] 执行验证命令，记录状态、首次失败和剩余核验范围。

Outcome:

构造持有归一化配置值，随后修改调用者副本不会改变签发或验证所用的算法与 issuer。

## 实施证据

- [TestJWTManagerKeepsNormalizedConfigSnapshot](../../../security/jwt/jwt_test.go#L328)

[批次验证与基准](../review-94305741/README.md)
