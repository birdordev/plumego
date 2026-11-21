# Card 2000 — 核对多收集器 Clear 与 Record 交错的子顺序

Milestone: 稳定模块质量复审
Recipe: specs/change-recipes/analysis-only.yaml
Context Package: review
Priority: P3
State: done
Primary Module: metrics
Owned Files: `metrics/concurrency_regression_test.go`
Depends On: 无

Goal: 记录每个子项可见顺序和最终计数，仅断言已约定并发语义。
Scope: 用可阻塞子收集器控制 Clear 之间插入一条普通记录。
Non-goals: 维持稳定公开签名与标准库依赖，将验证限于本卡范围。
Files: `metrics/concurrency_regression_test.go`。
Tests: 记录每个子项可见顺序和最终计数，仅断言已约定并发语义。
Docs Sync: 记录核验结论；只在现有契约文字需要澄清时另提文档变更。
Done Definition: 完成下列验收，记录验证命令、状态及剩余限制。
Outcome: 在子项重置间插入一次记录后，首子项为 1、次子项为 0、合并为 1，符合逐子重置合同。

## 证据与影响

第 9 节检查号为 28,31,44,47，当前证据状态为补证。 [质量规则第 9 节](../../../docs/operations/agent-code-quality-rules.md#L267)

Clear 会逐个清空已注册收集器。是否支持跨子项同一时刻清空应由调用路径和现有文档决定。 [metrics/multi.go:120](../../../metrics/multi.go#L120)

[复审报告](../../../docs/evidence/stable-quality-review-999d5e4d.md)

## 验收与验证

- [x] 记录每个子项可见顺序和最终计数，仅断言已约定并发语义。
- [x] 记录挂载入口、具体断言和结果。已有测试能覆盖时复用并注明。

## 实施证据

子收集器在自身清空后用通道阻塞 Multi.Clear。此时提交一条记录，再释放父清空到下一子项。GetStats 最终返回 1，分别查看两个子项为 1 与 0。 探针通过，临时 overlay 位于工作区外。

```sh
go test -overlay=/tmp/999d-analysis-overlay.json -race -v -timeout 45s ./metrics -run '^TestReviewMultiClearInterleaving$'
```
