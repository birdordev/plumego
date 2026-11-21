# Card 1753 — 核对 Ready 被 Goexit 终止后的阶段恢复

Milestone: 稳定模块质量复审
Recipe: specs/change-recipes/add-acceptance-tests.yaml
Context Package: implementation
Priority: P2
State: done
Primary Module: core
Owned Files: `core/lifecycle_group_test.go`
Depends On: 无

Goal: 退出可观察，状态不挂在 probing，正确清理一次。
Scope: 在独立测试 goroutine 用退出通道验证 Ready 后 Stop 和再次 Ready。
Non-goals: 维持稳定公开签名与标准库依赖，将验证限于本卡范围。
Files: `core/lifecycle_group_test.go`。
Tests: 退出可观察，状态不挂在 probing，正确清理一次。
Docs Sync: 复核本卡涉及的已公开约定。
Done Definition: 完成下列验收，记录验证命令、状态及剩余限制。
Outcome: Ready 回调 Goexit 后释放 probing 状态。下一次探测和 Stop 正常，清理只调用一次。

## 证据与影响

第 9 节检查号为 28,29,33,36,47。证据状态为补证。

Ready 进入 probing 后靠 defer 归位，上一批覆盖 panic；回调主动结束 goroutine 仍需单独锁定。 [core/lifecycle_group.go:289](../../../core/lifecycle_group.go#L289)

[复审报告](../../../docs/evidence/stable-quality-review-d224acbc.md)

## 验收与验证

- [x] 退出可观察，状态不挂在 probing，正确清理一次。
- [x] 记录运行结果。若已有用例覆盖目标，记录用例及相关断言。

```sh
go test -race -timeout 60s ./core
```

## 实施证据

- [TestReadyGoexitRestoresRunningPhase](../../../core/lifecycle_group_test.go#L698)

[批次验证与基准](../review-d224acbc/README.md)
