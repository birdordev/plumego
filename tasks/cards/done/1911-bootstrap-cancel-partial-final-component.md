# Card 1911 — 核对最后一个 Start 回调结束时触发取消的回滚

Milestone: 稳定模块质量复审
Recipe: specs/change-recipes/add-acceptance-tests.yaml
Context Package: implementation
Priority: P2
State: done
Primary Module: core
Owned Files: `core/lifecycle_group_test.go`
Depends On: 无

Goal: 组件最终状态与启动失败一致，已清理项不会重复停，退出保留对应分类。
Scope: 最后回调取消父 Context 后返回 nil，停止回调记录传入原因与逆序；单项首次失败后再 Stop。
Non-goals: 维持稳定公开签名与标准库依赖，将验证限于本卡范围。
Files: `core/lifecycle_group_test.go`。
Tests: 组件最终状态与启动失败一致，已清理项不会重复停，退出保留对应分类。
Docs Sync: 记录核验结论；只在现有契约文字需要澄清时另提文档变更。
Done Definition: 完成下列验收，记录验证命令、状态及剩余限制。
Outcome: 最后一个 Start 内取消会逆序回滚所有已获得组件。失败的回滚原因和 Context 原因均可识别，后续 Stop 只重试失败项。

## 证据与影响

第 9 节检查号为 26,28,30,31,36。证据状态为补证。

遍历最后返回时额外检查 Context。需要固定最后回调已获取资源但返回 nil 与取消重叠时，所有 pending 组件的归属。 [core/lifecycle_group.go:204](../../../core/lifecycle_group.go#L204)

[复审报告](../../../docs/evidence/stable-quality-review-2b94cf86.md)

## 验收与验证

- [x] 组件最终状态与启动失败一致，已清理项不会重复停，退出保留对应分类。
- [x] 记录挂载入口、具体断言和结果。已有测试能覆盖时复用并注明。

```sh
go test -race -timeout 60s ./core
```

## 实施证据

- [TestCancellationInLastStartRollsBackAllAcquiredComponents](../../../core/lifecycle_group_test.go#L816)

[批次验证与基准](../review-2b94cf86/README.md)
