# Card 1889 — 补证就绪 Clone 的 nil 和空组件 map 语义

Milestone: 稳定模块质量复审
Recipe: specs/change-recipes/add-acceptance-tests.yaml
Context Package: implementation
Priority: P2
State: done
Primary Module: health
Owned Files: `health/health_test.go`
Depends On: 无

Goal: Go 层形状和独立所有权可观察，JSON 空组件位沿用现有约定。
Scope: 对零 map、已分配空 map 和带 false 的 map 各生成两个快照。
Non-goals: 维持稳定公开签名与标准库依赖，将验证限于本卡范围。
Files: `health/health_test.go`。
Tests: Go 层形状和独立所有权可观察，JSON 空组件位沿用现有约定。
Docs Sync: 记录核验结论；只在现有契约文字需要澄清时另提文档变更。
Done Definition: 完成下列验收，记录验证命令、状态及剩余限制。
Outcome: Clone 在 Go 中区分 nil 与可写空 map，JSON 对两种空容器均省略组件字段。

## 证据与影响

第 9 节检查号为 6,16,34,42。证据状态为补证。

Clone 复制组件 map，JSON omitempty 可能对两类空 map 采用同样 wire 形状。需联测 Go 返回值及空 map 可写性。 [health/readiness.go:18](../../../health/readiness.go#L18)

[复审报告](../../../docs/evidence/stable-quality-review-a8c2f28a.md)

## 验收与验证

- [x] Go 层形状和独立所有权可观察，JSON 空组件位沿用现有约定。
- [x] 记录挂载入口、具体断言和结果。已有测试能覆盖时复用并注明。

```sh
go test -race -timeout 60s ./health
```

## 实施证据

- [TestReadinessClonesKeepNilDistinctFromWritableEmptyMap](../../../health/health_test.go#L198)

[批次验证与基准](../review-a8c2f28a/README.md)
