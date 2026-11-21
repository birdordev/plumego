# Card 1679 — 厘清 File Error.Error 的路径和底层文案契约

Milestone: 稳定模块质量复审
Recipe: specs/change-recipes/add-acceptance-tests.yaml
Context Package: implementation
Priority: P2
State: done
Primary Module: store
Owned Files: `store/file/coverage_test.go`, `docs/modules/store/README.md`, `website/src/generated/modules.ts`
Depends On: 无

Goal: 厘清 File Error.Error 的路径和底层文案契约。
Scope: 核对典型适配器如何分类，再用占位路径和错误验证调用者应对外使用的字段。
Non-goals: 维持公开签名和依赖；涉及现有兼容契约的调整须先核对调用边界及迁移影响。
Files: `store/file/coverage_test.go`, `docs/modules/store/README.md`, `website/src/generated/modules.ts`。
Tests: 保留错误链分类，运输层使用所属模块的安全映射。
Docs Sync: 复核 `docs/modules/store/README.md`，模块说明改动后核对生成文件差异。
Done Definition: 完成测试与验收记录，并标明剩余取证范围。
Outcome: 保留 File Error 的路径和错误链行为。模块 primer 说明传输侧应以分类和固定文本写错误。

## 证据

检查号为 16,22,25,41，状态为补证。

文件错误包含路径和底层文本，同时实现 Unwrap。用于传输层前需要知道安全输出边界。 [store/file/errors.go:54](../../../store/file/errors.go#L54)

[复审报告](../../../docs/evidence/stable-quality-review-09a7f5d3.md)

## 验收与验证

- [x] 保留错误链分类，运输层使用所属模块的安全映射。
- [x] 记录验证命令、状态和剩余范围。若使用既有用例满足验收，记录对应证据。

```sh
go test -race -timeout 60s ./store/file
make website-sync
make website-check-generated
```

## 实施证据

[TestError_Error_WithPath](../../../store/file/coverage_test.go#L36) · [TestError_Unwrap](../../../store/file/coverage_test.go#L66)
