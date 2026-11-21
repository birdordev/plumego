# Card 1668 — 核对 JWT 时钟和宽限窗口边界

Milestone: 稳定模块质量复审
Recipe: specs/change-recipes/add-acceptance-tests.yaml
Context Package: implementation
Priority: P2
State: done
Primary Module: security
Owned Files: `security/jwt/jwt_verify_test.go`
Depends On: 无

Goal: 核对 JWT 时钟和宽限窗口边界。
Scope: 用现有本地签发测试夹具及可控时钟，验证正常、边界和超期。
Non-goals: 维持公开签名和依赖；涉及现有兼容契约的调整须先核对调用边界及迁移影响。
Files: `security/jwt/jwt_verify_test.go`。
Tests: 错误码和文档窗口相符，验证结果无凭据正文。
Docs Sync: 对照原契约核验，若证据要求修订公开行为，先更新实施范围。
Done Definition: 完成测试与验收记录，并标明剩余取证范围。
Outcome: 用固定时钟替换 NotBefore 测试中的睡眠，观察宽限精确边界；复用过期回归。

## 证据

检查号为 6,14,40,46，状态为补证。

JWT 校验多个时钟与声明条件，声明边界和误差策略影响拒绝结果。 [security/jwt/verify.go:11](../../../security/jwt/verify.go#L11)

[复审报告](../../../docs/evidence/stable-quality-review-09a7f5d3.md)

## 验收与验证

- [x] 错误码和文档窗口相符，验证结果无凭据正文。
- [x] 记录验证命令、状态和剩余范围。若使用既有用例满足验收，记录对应证据。

```sh
go test -race -timeout 60s ./security/jwt
```

## 实施证据

[TestNotBeforeWithClockSkew](../../../security/jwt/jwt_verify_test.go#L41) · [TestClockSkewTolerance](../../../security/jwt/jwt_verify_test.go#L10)
