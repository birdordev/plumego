# Card 2016 — 就绪回调异常退出释放生命周期阶段

Milestone: 稳定模块第 9 节复审
Recipe: specs/change-recipes/add-acceptance-tests.yaml
Context Package: implementation
Priority: P2
State: done
Primary Module: core
Owned Files: `core/lifecycle_group_test.go`
Depends On: 无

Goal: 就绪回调异常退出释放生命周期阶段
Scope: 在受控 goroutine 中结束首次探测，然后发起 Stop。
Non-goals: 范围限于本卡涉及的契约，保持公开签名和依赖。
Files: `core/lifecycle_group_test.go`。
Tests: 探测线程退出后可正常停止已启动的资源，后续探测失败。
Docs Sync: 核对新增验收与已有模块说明一致，记录证据和限制。
Done Definition: 完成以下验收，记录命令、结果与剩余限制。
Outcome: Ready 回调 Goexit 后阶段释放，Stop 只执行一次，停止后 Ready 返回错误。

## 证据与影响

证据状态为补证。第 9 节检查号为 28,29,33,36,47。 [质量规则第 9 节](../../../docs/operations/agent-code-quality-rules.md#L267)

Ready 使用 defer 还原阶段。检验回调 runtime.Goexit 与正常 panic 的退出边界。 [core/lifecycle_group.go:289](../../../core/lifecycle_group.go#L289)

[复审报告](../../../docs/evidence/stable-quality-review-c3a77410.md)

## 验收与验证

- [x] 探测线程退出后可正常停止已启动的资源，后续探测失败。
- [x] 已有测试覆盖相同触发和断言时可直接复用，记录具体用例。

```sh
go test -race -timeout 60s ./core
```

## 实施证据

- [TestReadyGoexitReleasesLifecyclePhase](../../../core/lifecycle_group_test.go#L890)
