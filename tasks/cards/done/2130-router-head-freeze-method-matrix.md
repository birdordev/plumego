# Card 2130 — 核对 HEAD 与 Allow 在预热和冻结后的决策矩阵

Milestone: 稳定模块第 9 节复审
Recipe: specs/change-recipes/analysis-only.yaml
Context Package: review
Priority: P2
State: done
Primary Module: router
Owned Files: `router/router_contract_test.go`
Depends On: 无

Goal: 核对 HEAD 与 Allow 在预热和冻结后的决策矩阵。
Scope: 建立同一组路由的冷态、预热后、冻结后三类对照，固定 handler、匹配名、404/405 和 Allow 的断言。
Non-goals: 本卡提交取证结果。后续行为变更由独立修复卡执行。
Files: `router/router_contract_test.go`
Tests: 以同一张表覆盖显式 HEAD、GET 回退、自定义方法、完全不匹配。
Docs Sync: 核对既有文档与验收结果一致，记录需要后续同步的位置。
Done Definition: 记录命令、结果、证据限制和是否需要独立修复卡。
Outcome: 临时公开入口测试对比初次、预热以及冻结后三阶段。HEAD 保留显式路由或 GET 完整回退，REPORT 通配命中，405 Allow 排序、404 和 HEAD 空体一致。

## 证据与影响

证据状态为待补证。第 9 节检查号为 19,23,24,32,37,45,47。已有独立测试覆盖回退、冻结和 Allow。显式 HEAD、GET 路径死支、通配完整匹配与冻结预热的四路组合待核对。 [router/dispatch.go:188](../../../router/dispatch.go#L188) [router/dispatch.go:209](../../../router/dispatch.go#L209) [router/router_contract_test.go:1624](../../../router/router_contract_test.go#L1624) [router/router_contract_test.go:1710](../../../router/router_contract_test.go#L1710)

[复审报告](../../../docs/evidence/stable-quality-review-6e8d53f9.md)

## 验收与验证

- [x] 以同一张表覆盖显式 HEAD、GET 回退、自定义方法、完全不匹配。
- [x] 记录命令、结果、证据限制和是否需要独立修复卡。

```sh
go test -race -timeout 60s ./router
```
