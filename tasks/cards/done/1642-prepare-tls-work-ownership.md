# Card 1642 — 并发 Prepare 单次发布但可重复读 TLS 文件

Milestone: 稳定模块质量复审
Recipe: specs/change-recipes/analysis-only.yaml
Context Package: review
Priority: P2
State: done
Primary Module: core
Owned Files: `core/http_handler.go`, `core/core_public_test.go`, `docs/modules/core/README.md`, `website/src/generated/modules.ts`
Depends On: 无

Goal: 并发 Prepare 单次发布但可重复读 TLS 文件。
Scope: 测量有证书场景后考虑为 server-only 阶段单独预留资格，保留外部构建器可重入与读取失败后的重试。
Non-goals: 维持公开签名和依赖；涉及现有兼容契约的调整须先核对调用边界及迁移影响。
Files: `core/http_handler.go`, `core/core_public_test.go`, `docs/modules/core/README.md`, `website/src/generated/modules.ts`。
Tests: 受控并发验证已发布指针唯一、证书读取次数及失败不冻结的顺序。
Docs Sync: 复核 `docs/modules/core/README.md`，模块说明改动后核对生成文件差异。
Done Definition: 完成测试与验收记录，并标明剩余取证范围。
Outcome: 4 个并发 TLS Prepare 仍只发布同一个服务器。基准测出额外分配，文档保留并说明证书读取可重叠；未改变准备重试及锁范围。

## 证据

检查号为 14,28,31,36,48，状态为源码确认。

开始时仅快照已发布服务器和中间件构建状态，确认证书后才尝试构建。并发成功只会安装一个 server，但未完成的调用可各自做文件 I/O。 [core/http_handler.go:93](../../../core/http_handler.go#L93)

[复审报告](../../../docs/evidence/stable-quality-review-09a7f5d3.md)

## 验收与验证

- [x] 受控并发验证已发布指针唯一、证书读取次数及失败不冻结的顺序。
- [x] 记录验证命令、状态和剩余范围。若使用既有用例满足验收，记录对应证据。

```sh
go test -race -timeout 60s ./core
make website-sync
make website-check-generated
```

## 实施证据

[TestConcurrentTLSPreparationPublishesOneServer](../../../core/core_public_test.go#L578) · [TestPublicPrepareRetriesAfterTLSFilesAppear](../../../core/core_public_test.go#L503) · [BenchmarkConcurrentTLSPrepare](../../../core/core_public_test.go#L589)

执行了 TLS 并发基准，结果见 [批次记录](../review-09a7f5d3/README.md)。
