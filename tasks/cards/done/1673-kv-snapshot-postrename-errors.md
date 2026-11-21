# Card 1673 — 确认目录同步与目录关闭错误的职责

Milestone: 稳定模块质量复审
Recipe: specs/change-recipes/add-acceptance-tests.yaml
Context Package: implementation
Priority: P2
State: done
Primary Module: store
Owned Files: `store/kv/persistence.go`, `store/kv/state_regression_test.go`
Depends On: 无

Goal: 确认目录同步与目录关闭错误的职责。
Scope: 先核对有意义的目录句柄错误，再决定是否 join；复用已提交状态测试。
Non-goals: 维持公开签名和依赖；涉及现有兼容契约的调整须先核对调用边界及迁移影响。
Files: `store/kv/persistence.go`, `store/kv/state_regression_test.go`。
Tests: 区分 unsupported 平台、同步失败、关闭失败和已发布状态，重开一致。
Docs Sync: 对照原契约核验，若证据要求修订公开行为，先更新实施范围。
Done Definition: 完成测试与验收记录，并标明剩余取证范围。
Outcome: 目录同步与关闭失败均可分类，仍忽略已识别的不支持目录同步错误。已替换快照的既有重开契约保持。

## 证据

检查号为 22,29,30,31,41，状态为补证。

快照已改进临时文件回滚。目录同步后 Close 错误仍被忽略，属于提交后诊断而非回滚。 [store/kv/persistence.go:265](../../../store/kv/persistence.go#L265)

[复审报告](../../../docs/evidence/stable-quality-review-09a7f5d3.md)

## 验收与验证

- [x] 区分 unsupported 平台、同步失败、关闭失败和已发布状态，重开一致。
- [x] 记录验证命令、状态和剩余范围。若使用既有用例满足验收，记录对应证据。

```sh
go test -race -timeout 60s ./store/kv
```

## 实施证据

[TestDirectorySyncRetainsCloseErrors](../../../store/kv/state_regression_test.go#L480) · [TestDirectorySyncFailurePreservesPublishedState](../../../store/kv/state_regression_test.go#L18)
