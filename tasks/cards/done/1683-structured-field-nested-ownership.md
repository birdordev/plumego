# Card 1683 — 核对 WithFields 保留嵌套数据的复制约定

Milestone: 稳定模块质量复审
Recipe: specs/change-recipes/add-acceptance-tests.yaml
Context Package: implementation
Priority: P2
State: done
Primary Module: log
Owned Files: `log/logger_semantics_test.go`, `docs/modules/log/README.md`, `website/src/generated/modules.ts`
Depends On: 无

Goal: 核对 WithFields 保留嵌套数据的复制约定。
Scope: 对文本和 JSON 用顺序修改及两个快照取证，避免写入竞态。
Non-goals: 维持公开签名和依赖；涉及现有兼容契约的调整须先核对调用边界及迁移影响。
Files: `log/logger_semantics_test.go`, `docs/modules/log/README.md`, `website/src/generated/modules.ts`。
Tests: 记录浅或深复制范围、输出预期和调用者职责。
Docs Sync: 复核 `docs/modules/log/README.md`，模块说明改动后核对生成文件差异。
Done Definition: 完成测试与验收记录，并标明剩余取证范围。
Outcome: 文本及 JSON 的 WithFields 复制顶层 map。嵌套数据仍由调用者负责，使用串行变更明确可见时机。

## 证据

检查号为 16,32,34,43，状态为补证。

WithFields 合并顶层字段，但嵌套 map/slice 生命周期由消费和序列化时机决定。 [log/logger.go:110](../../../log/logger.go#L110)

[复审报告](../../../docs/evidence/stable-quality-review-09a7f5d3.md)

## 验收与验证

- [x] 记录浅或深复制范围、输出预期和调用者职责。
- [x] 记录验证命令、状态和剩余范围。若使用既有用例满足验收，记录对应证据。

```sh
go test -race -timeout 60s ./log
make website-sync
make website-check-generated
```

## 实施证据

[TestDerivedLoggerFieldsCopyOnlyTheTopLevel](../../../log/logger_semantics_test.go#L244)
