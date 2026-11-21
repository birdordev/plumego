# Card 1855 — 验证并发 Shutdown 的诊断所有权移交边界

Milestone: 稳定模块质量复审
Recipe: specs/change-recipes/add-acceptance-tests.yaml
Context Package: implementation
Priority: P2
State: done
Primary Module: core
Owned Files: `core/lifecycle_test.go`
Depends On: 无

Goal: 两个 Shutdown 保留各自退出原因，最后连接退出后无后台任务，记录是否存在诊断空窗。
Scope: 用两个独立 Context 和一条可控活动连接，记录 owner 取消后第二次等待及后续第三次调用。
Non-goals: 维持稳定公开签名与标准库依赖，将验证限于本卡范围。
Files: `core/lifecycle_test.go`。
Tests: 两个 Shutdown 保留各自退出原因，最后连接退出后无后台任务，记录是否存在诊断空窗。
Docs Sync: 记录核验结论；只在现有契约文字需要澄清时另提文档变更。
Done Definition: 完成下列验收，记录验证命令、状态及剩余限制。
Outcome: 首个 Shutdown 放弃后释放诊断所有权，已经等待的第二次调用不会接管。第三次可接管，连接退出后两个仍在等待的 Shutdown 正常收尾。

## 证据与影响

第 9 节检查号为 26,28,29,32,36。证据状态为补证。

activeDrain 只授予一个调用，失败的竞争者不会回头接管。首调用截止而另一调用仍等待的诊断空窗需取证。 [core/lifecycle.go:152](../../../core/lifecycle.go#L152)

[复审报告](../../../docs/evidence/stable-quality-review-a8c2f28a.md)

## 验收与验证

- [x] 两个 Shutdown 保留各自退出原因，最后连接退出后无后台任务，记录是否存在诊断空窗。
- [x] 记录挂载入口、具体断言和结果。已有测试能覆盖时复用并注明。

```sh
go test -race -timeout 60s ./core
```

## 实施证据

- [TestDrainOwnershipRequiresNewAttemptAfterOwnerCancellation](../../../core/lifecycle_test.go#L1311)
- [TestConcurrentShutdownLeavesDrainVacantUntilNewCaller](../../../core/lifecycle_test.go#L1427)

[批次验证与基准](../review-a8c2f28a/README.md)
