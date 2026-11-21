# Card 1864 — 核对 Print 快照期间注册与失败后的重新打印

Milestone: 稳定模块质量复审
Recipe: specs/change-recipes/add-acceptance-tests.yaml
Context Package: implementation
Priority: P3
State: done
Primary Module: router
Owned Files: `router/router_print_test.go`
Depends On: 无

Goal: 当次按旧快照停止，重试看到包括迟到路由的有序完整输出，迟到路由可正常分派。
Scope: 用回调 writer 在输出第一条路由行时注册迟到路由，令其后一次写失败，再用正常 writer 重试。
Non-goals: 维持稳定公开签名与标准库依赖，将验证限于本卡范围。
Files: `router/router_print_test.go`。
Tests: 当次按旧快照停止，重试看到包括迟到路由的有序完整输出，迟到路由可正常分派。
Docs Sync: 记录核验结论；只在现有契约文字需要澄清时另提文档变更。
Done Definition: 完成下列验收，记录验证命令、状态及剩余限制。
Outcome: 打印在首次写失败后停止；写入期间注册的新路由只出现在后续打印和正常分派。

## 证据与影响

第 9 节检查号为 6,16,29,34,37。证据状态为补证。

已有首个失败即停止的用例。需观察在写路由行时注册新路由，且该次打印随后失败的快照边界。 [router/metadata.go:203](../../../router/metadata.go#L203)

[复审报告](../../../docs/evidence/stable-quality-review-a8c2f28a.md)

## 验收与验证

- [x] 当次按旧快照停止，重试看到包括迟到路由的有序完整输出，迟到路由可正常分派。
- [x] 记录挂载入口、具体断言和结果。已有测试能覆盖时复用并注明。

```sh
go test -race -timeout 60s ./router
```

## 实施证据

- [TestPrintRetriesWithNewSnapshotAfterRegistrationAndFailure](../../../router/router_print_test.go#L91)

[批次验证与基准](../review-a8c2f28a/README.md)
