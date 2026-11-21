# Card 1863 — 核对 Freeze 前确定的 405 开关及冷热等价

Milestone: 稳定模块质量复审
Recipe: specs/change-recipes/add-acceptance-tests.yaml
Context Package: implementation
Priority: P2
State: done
Primary Module: router
Owned Files: `router/router_contract_test.go`
Depends On: 无

Goal: 已有成功分派保持一致，405 的 Allow 或 404 仍由冻结前所选政策决定。
Scope: 分别从开与关预热 GET、HEAD 并 Freeze，尝试切换后请求两类不匹配路径。
Non-goals: 维持稳定公开签名与标准库依赖，将验证限于本卡范围。
Files: `router/router_contract_test.go`。
Tests: 已有成功分派保持一致，405 的 Allow 或 404 仍由冻结前所选政策决定。
Docs Sync: 记录核验结论；只在现有契约文字需要澄清时另提文档变更。
Done Definition: 完成下列验收，记录验证命令、状态及剩余限制。
Outcome: 两种冻结前策略分别验证热路径，冻结后更新不会改变 Allow 或 404 选择。

## 证据与影响

第 9 节检查号为 6,28,32,37。证据状态为补证。

Freeze 后 SetMethodNotAllowed 忽略更新。已有状态位回归，需组合两种初始政策、已预热的 GET 和 HEAD 以及失败分派。 [router/router.go:185](../../../router/router.go#L185)

[复审报告](../../../docs/evidence/stable-quality-review-a8c2f28a.md)

## 验收与验证

- [x] 已有成功分派保持一致，405 的 Allow 或 404 仍由冻结前所选政策决定。
- [x] 记录挂载入口、具体断言和结果。已有测试能覆盖时复用并注明。

```sh
go test -race -timeout 60s ./router
```

## 实施证据

- [TestFrozenRouterKeepsPrewarmedAllowPolicy](../../../router/router_contract_test.go#L1624)

[批次验证与基准](../review-a8c2f28a/README.md)
