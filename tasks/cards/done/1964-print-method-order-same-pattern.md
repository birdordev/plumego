# Card 1964 — 锁定路由清单相同模式下的方法顺序

Milestone: 稳定模块质量复审
Recipe: specs/change-recipes/add-acceptance-tests.yaml
Context Package: implementation
Priority: P3
State: done
Primary Module: router
Owned Files: `router/router_print_test.go`
Depends On: 无

Goal: 记录稳定的排序键和重复运行结果，不依赖 map 遍历顺序。
Scope: 用反序注册同一模式的两种方法，比较冻结前后和跨实例输出。
Non-goals: 维持稳定公开签名与标准库依赖，将验证限于本卡范围。
Files: `router/router_print_test.go`。
Tests: 记录稳定的排序键和重复运行结果，不依赖 map 遍历顺序。
Docs Sync: 记录核验结论；只在现有契约文字需要澄清时另提文档变更。
Done Definition: 完成下列验收，记录验证命令、状态及剩余限制。
Outcome: 相同模式的打印清单依方法排序，反序注册、重复构造与冻结后结果一致。

## 证据与影响

第 9 节检查号为 14,32,37,52，当前证据状态为补证。 [质量规则第 9 节](../../../docs/operations/agent-code-quality-rules.md#L267)

打印使用单独的排序快照。同一路径多方法时，注册输入顺序不应改变文档所示排序。 [router/metadata.go:203](../../../router/metadata.go#L203)

[复审报告](../../../docs/evidence/stable-quality-review-999d5e4d.md)

## 验收与验证

- [x] 记录稳定的排序键和重复运行结果，不依赖 map 遍历顺序。
- [x] 记录挂载入口、具体断言和结果。已有测试能覆盖时复用并注明。

```sh
go test -race -timeout 60s ./router
```

## 实施证据

- [TestPrintOrdersSamePatternByMethodAcrossFreeze](../../../router/router_print_test.go#L118)
