# Card 1860 — 验证命名通配符反向路由对内部空段的往返

Milestone: 稳定模块质量复审
Recipe: specs/change-recipes/add-acceptance-tests.yaml
Context Package: implementation
Priority: P2
State: done
Primary Module: router
Owned Files: `router/router_contract_test.go`
Depends On: 无

Goal: 明确内部空段保留规则，非法首尾值分类不变。
Scope: 生成两个合法命名通配路径并在冷、热缓存下重新分派，检查原值与 Pattern。
Non-goals: 维持稳定公开签名与标准库依赖，将验证限于本卡范围。
Files: `router/router_contract_test.go`。
Tests: 明确内部空段保留规则，非法首尾值分类不变。
Docs Sync: 记录核验结论；只在现有契约文字需要澄清时另提文档变更。
Done Definition: 完成下列验收，记录验证命令、状态及剩余限制。
Outcome: 命名通配符的 Unicode 和内部空段在冷、热路径都按原值往返，首尾斜杠仍被拒绝。

## 证据与影响

第 9 节检查号为 6,18,37,46。证据状态为补证。

反向路由只拒绝通配值的首尾斜杠，匹配器按原始后缀捕获。内部连续斜杠及 Unicode 编码组合需要往返验证。 [router/metadata.go:113](../../../router/metadata.go#L113)

[复审报告](../../../docs/evidence/stable-quality-review-a8c2f28a.md)

## 验收与验证

- [x] 明确内部空段保留规则，非法首尾值分类不变。
- [x] 记录挂载入口、具体断言和结果。已有测试能覆盖时复用并注明。

```sh
go test -race -timeout 60s ./router
```

## 实施证据

- [TestNamedWildcardRoundTripsEncodedValueWithInnerEmptySegment](../../../router/router_contract_test.go#L1573)

[批次验证与基准](../review-a8c2f28a/README.md)
