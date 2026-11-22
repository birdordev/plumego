# Card 2125 — 合并重复的无名指标断言

Milestone: 稳定模块第 9 节复审
Recipe: specs/change-recipes/fix-bug.yaml
Context Package: implementation
Priority: P3
State: done
Primary Module: metrics
Owned Files: `metrics/collector_test.go`
Depends On: 无

Goal: 合并重复的无名指标断言。
Scope: 整合共同断言，保留通用名称、HTTP 名称、快照隔离和起始时间的独立断言，并按实际样本重命名 256 名称用例。
Non-goals: 保持公开签名、依赖和其余模块的行为。
Files: `metrics/collector_test.go`
Tests: 对照合并前的断言集合，核对功能矩阵和 race 测试。
Docs Sync: 本卡影响限于回归用例和注释。
Done Definition: 完成以下回归和验收，记录实际命令及结果。
Outcome: 无名计数场景合为通用名称与 HTTP 名称的两个子用例。保留分类、容量样本、旧快照独立与清空后的时间断言。metrics 包竞态测试通过。

## 证据与影响

证据状态为源码确认。第 9 节检查号为 9,12,44,45,47。两项回归都断言无名计数与 Clear。另一个用例名把固定样本规模称为容量边界，实际只写入 256 个名字。 [metrics/collector_test.go:243](../../../metrics/collector_test.go#L243) [metrics/collector_test.go:263](../../../metrics/collector_test.go#L263) [metrics/collector_test.go:295](../../../metrics/collector_test.go#L295)

[复审报告](../../../docs/evidence/stable-quality-review-6e8d53f9.md)

## 验收与验证

- [x] 对照合并前的断言集合，核对功能矩阵和 race 测试。
- [x] 完成以下回归和验收，记录实际命令及结果。

```sh
go test -race -timeout 60s ./metrics
```

实现时先运行 `make fmt`，按实际改动范围预览并运行 diff 门禁。
