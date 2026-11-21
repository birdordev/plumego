# Card 1570 — 中间件构建重入导致应用准备阻塞

Milestone: 本轮质量审查
Recipe: specs/change-recipes/fix-bug.yaml
Context Package: implementation
Priority: P1
State: done
Primary Module: core
Owned Files: `core/app.go`, `core/http_handler.go`, `core/core_public_test.go`, `docs/modules/core/README.md`, `core/coverage_test.go`
Depends On: 无

Goal: 中间件构建重入导致应用准备阻塞。
Scope: 更新准备状态与原有准备锁的内部断言。用明确的构建中状态和单一发布边界取代跨构建器的准备锁。重叠尝试应有明确结果，失败或 panic 释放构建资格。
Non-goals: 保留现有公开签名和依赖。涉及未授权的行为或兼容契约调整时，先完成调用方核对与兼容决策。
Files: `core/app.go`, `core/http_handler.go`, `core/core_public_test.go`, `docs/modules/core/README.md`, `core/coverage_test.go`。
Tests: 用通道控制构建器重入 Prepare 和外部并发 Prepare，断言调用可返回、成功时仅发布一次，失败时仍可重试。
Docs Sync: 核对 `docs/modules/core/README.md`。 修改模块说明后检查网站生成差异，并在同一交付中保留相应生成文件。
Done Definition: 完成下列验收和验证，记录证据与剩余风险。
Outcome: 构建状态由 App 互斥锁保护，外部构建器在锁外运行；重入和重叠请求立即返回，普通无中间件的并发准备仍返回同一服务器。

## 证据与影响

- 检查号：20,28,33,36,47。证据状态：已复现。
- 外层 Prepare 持有 serverPrepareMu 并在 handlerPrepareMu 内调用构建器，构建器重入 Prepare 时等待自身持有的锁。临时测试超时，栈停在 ensureServerPrepared；首次 ServeHTTP 也会进入同一构建路径。 [core/http_handler.go:34](../../../core/http_handler.go#L34)
- [审查报告](../../../docs/evidence/stable-quality-review-dee7aa0f.md)

## 验收与验证

- [x] 用通道控制构建器重入 Prepare 和外部并发 Prepare，断言调用可返回、成功时仅发布一次，失败时仍可重试。
- [x] 运行以下命令并记录结果、失败路径和未验证范围。修改 Go 文件前完成仓库预检，验证前先执行仓库格式化。

```sh
go test -race -timeout 60s ./core
make website-sync
make website-check-generated
```

## 实施证据

[TestPublicMiddlewareBuilderCanReenterPreparation](../../../core/core_public_test.go#L447) · [TestPublicPreparationRetriesMiddlewarePanic](../../../core/core_public_test.go#L381) · [TestConcurrentPrepareReturnsSameServer](../../../core/lifecycle_test.go#L629)
