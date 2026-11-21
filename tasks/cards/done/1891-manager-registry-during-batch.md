# Card 1891 — 核对健康集合变更与批量探测结果的代际边界

Milestone: 稳定模块质量复审
Recipe: specs/change-recipes/analysis-only.yaml
Context Package: review
Priority: P2
State: done
Primary Module: x/observability/ops
Owned Files: `x/observability/ops/healthhttp/aggregate_regression_test.go`
Depends On: 无

Goal: 记录当前批与下一批组件与失败字段的归属，旧组件不得被复活。
Scope: 一批检查阻塞后发起正常注册或注销，再释放并执行下一批。
Non-goals: 维持稳定公开签名与标准库依赖，将验证限于本卡范围。
Files: `x/observability/ops/healthhttp/aggregate_regression_test.go`。
Tests: 记录当前批与下一批组件与失败字段的归属，旧组件不得被复活。
Docs Sync: 记录核验结论；只在现有契约文字需要澄清时另提文档变更。
Done Definition: 完成下列验收，记录验证命令、状态及剩余限制。
Outcome: 临时探针确认运行中注册的组件进入 readiness 但在本批未执行，注销组件不复活；下一批按新名册执行并恢复。

## 证据与影响

第 9 节检查号为 28,31,32,42。证据状态为补证。

批次快照的组件名与最终 health map 可能不同。已核对同名覆盖的组件版本，尚需核对批次中新增和移除时的 readiness。 [x/observability/ops/healthhttp/manager.go:181](../../../x/observability/ops/healthhttp/manager.go#L181)

[复审报告](../../../docs/evidence/stable-quality-review-a8c2f28a.md)

## 验收与验证

如果现有用例未覆盖该交叠，用工作区外临时桩或 Go overlay 补充取证。

- [x] 记录当前批与下一批组件与失败字段的归属，旧组件不得被复活。
- [x] 记录挂载入口、具体断言和结果。已有测试能覆盖时复用并注明。

## 实施证据

阻塞首次检查时登记新组件，分别保留及注销原组件。释放旧批后读取它的 Dependencies 和当前 Readiness，再运行下一批。保留路径旧批依赖仍为 first，新组件在这一批不就绪；注销路径只在当前就绪表出现 later，已注销组件不复活。

```sh
go test -race -timeout 30s /tmp/a8c-retention-boundaries_test.go -run '^TestRegistryChangesWhileBatchIsRunning$'
```

[批次验证与基准](../review-a8c2f28a/README.md)
