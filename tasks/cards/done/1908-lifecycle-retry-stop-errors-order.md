# Card 1908 — 核对多组件停止失败与重试的错误顺序

Milestone: 稳定模块质量复审
Recipe: specs/change-recipes/add-acceptance-tests.yaml
Context Package: implementation
Priority: P2
State: done
Primary Module: core
Owned Files: `core/lifecycle_group_test.go`
Depends On: 无

Goal: 第一次保留两个原因及明确顺序，重试只收尾未完成项。
Scope: 让 A、B、C 中两个各失败一次且返回不同原因，同一次 Stop 观察 Join 顺序，再验证成功重试。
Non-goals: 维持稳定公开签名与标准库依赖，将验证限于本卡范围。
Files: `core/lifecycle_group_test.go`。
Tests: 第一次保留两个原因及明确顺序，重试只收尾未完成项。
Docs Sync: 记录核验结论；只在现有契约文字需要澄清时另提文档变更。
Done Definition: 完成下列验收，记录验证命令、状态及剩余限制。
Outcome: 第一次按逆序保留两个标准可分类的停止原因；后续只按逆序处理还未成功的组件。

## 证据与影响

第 9 节检查号为 22,28,30,31,36。证据状态为补证。

停止按逆序收集错误，只留下失败项。需同一夹具核对多次失败顺序和标准原因仍可分类。 [core/lifecycle_group.go:235](../../../core/lifecycle_group.go#L235)

[复审报告](../../../docs/evidence/stable-quality-review-2b94cf86.md)

## 验收与验证

- [x] 第一次保留两个原因及明确顺序，重试只收尾未完成项。
- [x] 记录挂载入口、具体断言和结果。已有测试能覆盖时复用并注明。

```sh
go test -race -timeout 60s ./core
```

## 实施证据

- [TestStopRetriesOrderedFailedComponentsOnly](../../../core/lifecycle_group_test.go#L791)

[批次验证与基准](../review-2b94cf86/README.md)
