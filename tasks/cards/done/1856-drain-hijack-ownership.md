# Card 1856 — 联测连接接管后的排空计数及资源归属

Milestone: 稳定模块质量复审
Recipe: specs/change-recipes/add-acceptance-tests.yaml
Context Package: implementation
Priority: P2
State: done
Primary Module: core
Owned Files: `core/lifecycle_test.go`
Depends On: 无

Goal: 普通连接继续属于排空，接管连接由使用者关闭，计数不重复扣减。
Scope: 使用本机监听器，保持一条普通连接活跃，另一条成功接管后由 handler 管理，记录 shutdown 开始与退出。
Non-goals: 维持稳定公开签名与标准库依赖，将验证限于本卡范围。
Files: `core/lifecycle_test.go`。
Tests: 普通连接继续属于排空，接管连接由使用者关闭，计数不重复扣减。
Docs Sync: 记录核验结论；只在现有契约文字需要澄清时另提文档变更。
Done Definition: 完成下列验收，记录验证命令、状态及剩余限制。
Outcome: 真实 loopback 中接管后的连接不计入 HTTP 排空。普通连接阻止首轮收尾，重试完成后接管连接仍由双方直接通信。

## 证据与影响

第 9 节检查号为 24,28,29,36。证据状态为补证。

追踪将 StateHijacked 与 StateClosed 都视作离开 HTTP 管理。应对照同一真实监听器的两类连接。 [core/lifecycle.go:126](../../../core/lifecycle.go#L126)

[复审报告](../../../docs/evidence/stable-quality-review-a8c2f28a.md)

## 验收与验证

- [x] 普通连接继续属于排空，接管连接由使用者关闭，计数不重复扣减。
- [x] 记录挂载入口、具体断言和结果。已有测试能覆盖时复用并注明。

```sh
go test -race -timeout 60s ./core
```

## 实施证据

- [TestAppShutdownDoesNotOwnHijackedConnection](../../../core/lifecycle_test.go#L1345)

[批次验证与基准](../review-a8c2f28a/README.md)
