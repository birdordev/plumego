# Card 1654 — 核对 HEAD 和 Trailer 声明的组合行为

Milestone: 稳定模块质量复审
Recipe: specs/change-recipes/add-acceptance-tests.yaml
Context Package: implementation
Priority: P2
State: done
Primary Module: router
Owned Files: `router/head_regression_test.go`
Depends On: 无

Goal: 核对 HEAD 和 Trailer 声明的组合行为。
Scope: 编排显式声明和动态前缀的本地客户端对照。
Non-goals: 维持公开签名和依赖；涉及现有兼容契约的调整须先核对调用边界及迁移影响。
Files: `router/head_regression_test.go`。
Tests: 比较 GET 与 HEAD 状态、正文和长度语义。
Docs Sync: 对照原契约核验，若证据要求修订公开行为，先更新实施范围。
Done Definition: 完成测试与验收记录，并标明剩余取证范围。
Outcome: 真实 GET 接收 trailers，HEAD 保持空正文，尾字段和推断长度不进入最终头。

## 证据

检查号为 23,24,37，状态为补证。

路由经过有声明 trailers 的 HEAD 时跳过长度推断。需要确认真实 HTTP 传输可见头，以及原 handler 的尾字段不会作为普通管线头泄漏。 [router/dispatch.go:92](../../../router/dispatch.go#L92)

[复审报告](../../../docs/evidence/stable-quality-review-09a7f5d3.md)

## 验收与验证

- [x] 比较 GET 与 HEAD 状态、正文和长度语义。
- [x] 记录验证命令、状态和剩余范围。若使用既有用例满足验收，记录对应证据。

```sh
go test -race -timeout 60s ./router
```

## 实施证据

[TestHeadDeclaredTrailersDoNotBecomeResponseFields](../../../router/head_regression_test.go#L396)
