# Card 1634 — 独立 Chain 的 nil builder 在 Build 时 panic

Milestone: 稳定模块质量复审
Recipe: specs/change-recipes/fix-bug.yaml
Context Package: implementation
Priority: P2
State: done
Primary Module: middleware
Owned Files: `middleware/middleware.go`, `middleware/middleware_test.go`, `docs/modules/middleware/README.md`, `website/src/generated/modules.ts`
Depends On: 无

Goal: 独立 Chain 的 nil builder 在 Build 时 panic。
Scope: 确认独立 Chain 对 nil builder 的策略，在 Build 入口统一以既有 nil handler 失败方式报告，补齐 GoDoc 和调用方判空。
Non-goals: 维持公开签名和依赖；涉及现有兼容契约的调整须先核对调用边界及迁移影响。
Files: `middleware/middleware.go`, `middleware/middleware_test.go`, `docs/modules/middleware/README.md`, `website/src/generated/modules.ts`。
Tests: 对 NewChain 与 Use 两种输入检查 Build 的结果，确认外层构建函数不会运行，健康链仍按顺序执行。
Docs Sync: 复核 `docs/modules/middleware/README.md`，模块说明改动后核对生成文件差异。
Done Definition: 完成测试与验收记录，并标明剩余取证范围。
Outcome: Chain.Build 先验证 builder 列表；两种方式加入 nil 均返回 nil 且不调用其它回调。

## 证据

检查号为 6,15,20,45，状态为已复现。

独立 NewChain 和 Use 接受空中间件，Build 直接调用空函数产生运行时 panic。App.Use 自己拒绝 nil 只保护 core 装配。 [middleware/middleware.go:131](../../../middleware/middleware.go#L131)

[复审报告](../../../docs/evidence/stable-quality-review-09a7f5d3.md)

## 验收与验证

- [x] 对 NewChain 与 Use 两种输入检查 Build 的结果，确认外层构建函数不会运行，健康链仍按顺序执行。
- [x] 记录验证命令、状态和剩余范围。若使用既有用例满足验收，记录对应证据。

```sh
go test -race -timeout 60s ./middleware
make website-sync
make website-check-generated
```

## 实施证据

[TestChainBuildRejectsNilMiddlewareBeforeCallingBuilders](../../../middleware/middleware_test.go#L194) · [TestChainBuildIsImmutableSnapshot](../../../middleware/middleware_test.go#L75)
