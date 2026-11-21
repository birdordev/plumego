# Card 1645 — 验证中间件构建异常退出释放资格

Milestone: 稳定模块质量复审
Recipe: specs/change-recipes/add-acceptance-tests.yaml
Context Package: implementation
Priority: P2
State: done
Primary Module: core
Owned Files: `core/core_public_test.go`
Depends On: 无

Goal: 验证中间件构建异常退出释放资格。
Scope: 在独立 goroutine 构建一次后退出，从公开 Prepare 重试。
Non-goals: 维持公开签名和依赖；涉及现有兼容契约的调整须先核对调用边界及迁移影响。
Files: `core/core_public_test.go`。
Tests: 验证冻结保留、失败阶段未发布、后续只成功构建一次。
Docs Sync: 对照原契约核验，若证据要求修订公开行为，先更新实施范围。
Done Definition: 完成测试与验收记录，并标明剩余取证范围。
Outcome: 构建回调终止 goroutine 后未发布半成品，注册继续冻结，公开 Prepare 重试成功。

## 证据

检查号为 28,29,33,36,46，状态为补证。

现有 nil 返回和 panic 回归已通过。Goexit 会执行 defer，但没有公开入口的独立状态证据。 [core/http_handler.go:50](../../../core/http_handler.go#L50)

[复审报告](../../../docs/evidence/stable-quality-review-09a7f5d3.md)

## 验收与验证

- [x] 验证冻结保留、失败阶段未发布、后续只成功构建一次。
- [x] 记录验证命令、状态和剩余范围。若使用既有用例满足验收，记录对应证据。

```sh
go test -race -timeout 60s ./core
```

## 实施证据

[TestPublicPrepareRetriesAfterBuilderGoexit](../../../core/core_public_test.go#L542)
