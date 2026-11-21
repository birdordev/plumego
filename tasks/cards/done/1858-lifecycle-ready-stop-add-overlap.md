# Card 1858 — 核对 Ready 回调期间 Stop、Add 和再次 Ready 的冲突处理

Milestone: 稳定模块质量复审
Recipe: specs/change-recipes/add-acceptance-tests.yaml
Context Package: implementation
Priority: P2
State: done
Primary Module: core
Owned Files: `core/lifecycle_group_test.go`
Depends On: 无

Goal: 操作重叠返回及时，未添加迟到组件，一次探测和一次合法收尾的顺序固定。
Scope: 阻塞第一个探测回调，让其他公开操作在屏障内返回，再释放并正常 Stop。
Non-goals: 维持稳定公开签名与标准库依赖，将验证限于本卡范围。
Files: `core/lifecycle_group_test.go`。
Tests: 操作重叠返回及时，未添加迟到组件，一次探测和一次合法收尾的顺序固定。
Docs Sync: 记录核验结论；只在现有契约文字需要澄清时另提文档变更。
Done Definition: 完成下列验收，记录验证命令、状态及剩余限制。
Outcome: Ready 回调持有 probing 阶段，重叠 Ready 和 Stop 及时返回，迟到 Add 不改变最终清理清单。

## 证据与影响

第 9 节检查号为 28,29,33,36,47。证据状态为补证。

已有单独 panic、Goexit、取消测试。需要在 probing 阶段对照另一个 Ready、Add 和 Stop，记录成功返回后的清理归属。 [core/lifecycle_group.go:287](../../../core/lifecycle_group.go#L287)

[复审报告](../../../docs/evidence/stable-quality-review-a8c2f28a.md)

## 验收与验证

- [x] 操作重叠返回及时，未添加迟到组件，一次探测和一次合法收尾的顺序固定。
- [x] 记录挂载入口、具体断言和结果。已有测试能覆盖时复用并注明。

```sh
go test -race -timeout 60s ./core
```

## 实施证据

- [TestLifecycleReadyOwnsPhaseUntilCallbackReturns](../../../core/lifecycle_group_test.go#L764)

[批次验证与基准](../review-a8c2f28a/README.md)
