# Card 1659 — 核对认证器返回新请求时的上下文继承

Milestone: 稳定模块质量复审
Recipe: specs/change-recipes/add-acceptance-tests.yaml
Context Package: implementation
Priority: P2
State: done
Primary Module: middleware
Owned Files: `middleware/auth/auth_test.go`, `security/authn/authn.go`
Depends On: 无

Goal: 核对认证器返回新请求时的上下文继承。
Scope: 用受控认证器派生上下文，并对照当前使用的适配入口。
Non-goals: 维持公开签名和依赖；涉及现有兼容契约的调整须先核对调用边界及迁移影响。
Files: `middleware/auth/auth_test.go`, `security/authn/authn.go`。
Tests: 正常派生保留取消及请求 ID；空请求使用原字段；拒绝时不调用下游。
Docs Sync: 对照原契约核验，若证据要求修订公开行为，先更新实施范围。
Done Definition: 完成测试与验收记录，并标明剩余取证范围。
Outcome: 认证器派生请求保留原取消和请求 ID，空响应回退及拒绝路径用原有回归核验。

## 证据

检查号为 20,26,27,39,40，状态为补证。

认证器可提供新请求，适配器采用其 Context。需明确外层取消、请求 ID 与注入身份的保留责任。 [middleware/auth/auth.go:81](../../../middleware/auth/auth.go#L81)

[复审报告](../../../docs/evidence/stable-quality-review-09a7f5d3.md)

## 验收与验证

- [x] 正常派生保留取消及请求 ID；空请求使用原字段；拒绝时不调用下游。
- [x] 记录验证命令、状态和剩余范围。若使用既有用例满足验收，记录对应证据。

```sh
go test -race -timeout 60s ./middleware/auth
```

## 实施证据

[TestAuthenticateMiddlewareUsesEnrichedRequestContext](../../../middleware/auth/auth_test.go#L157) · [TestAuthenticateEnrichedNilRequest](../../../middleware/auth/auth_test.go#L307) · [TestAuthenticateMiddlewareInvalidToken](../../../middleware/auth/auth_test.go#L89)
