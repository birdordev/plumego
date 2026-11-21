# Card 1648 — 验证 Ready 回调 panic 后组可继续 Stop

Milestone: 稳定模块质量复审
Recipe: specs/change-recipes/add-acceptance-tests.yaml
Context Package: implementation
Priority: P2
State: done
Primary Module: core
Owned Files: `core/lifecycle_group_test.go`
Depends On: 无

Goal: 验证 Ready 回调 panic 后组可继续 Stop。
Scope: 构造两个组件，用恢复器观测第一次 Ready panic，随后 Stop。
Non-goals: 维持公开签名和依赖；涉及现有兼容契约的调整须先核对调用边界及迁移影响。
Files: `core/lifecycle_group_test.go`。
Tests: panic 保持原值，关停逆序，后续 Ready 不就绪。
Docs Sync: 对照原契约核验，若证据要求修订公开行为，先更新实施范围。
Done Definition: 完成测试与验收记录，并标明剩余取证范围。
Outcome: Ready 原 panic 传播，组退出 probing 后可逆序关停。

## 证据

检查号为 28,29,33,36，状态为补证。

探测阶段通过 defer 恢复运行状态，回调抛异常时需要确认不会留下永久 probing。 [core/lifecycle_group.go:289](../../../core/lifecycle_group.go#L289)

[复审报告](../../../docs/evidence/stable-quality-review-09a7f5d3.md)

## 验收与验证

- [x] panic 保持原值，关停逆序，后续 Ready 不就绪。
- [x] 记录验证命令、状态和剩余范围。若使用既有用例满足验收，记录对应证据。

```sh
go test -race -timeout 60s ./core
```

## 实施证据

[TestReadyPanicRestoresGroupBeforeStop](../../../core/lifecycle_group_test.go#L651)
