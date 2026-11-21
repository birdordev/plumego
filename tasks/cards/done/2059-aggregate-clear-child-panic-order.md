# Card 2059 — 聚合清空遇子回调异常的边界

Milestone: 稳定模块第 9 节复审
Recipe: specs/change-recipes/analysis-only.yaml
Context Package: review
Priority: P2
State: done
Primary Module: metrics
Owned Files: `metrics/multi_test.go`
Depends On: 无

Goal: 聚合清空遇子回调异常的边界
Scope: 三个受控子收集器使第二个清空回调抛异常，恢复后分别读取子统计。
Non-goals: 范围限于本卡涉及的契约，保持公开签名和依赖。
Files: `metrics/multi_test.go`。
Tests: 记录已清空和未调用者的状态、异常传播与后续重试语义。需要修改时单列契约。
Docs Sync: 核对新增验收与已有模块说明一致，记录证据和限制。
Done Definition: 完成以下验收，记录命令、结果与剩余限制。
Outcome: 仅分析。第二子项抛异常时已完成的是第一个子项，异常传播，修复回调后可从头重试。

## 证据与影响

证据状态为补证。第 9 节检查号为 28,31,33,44,46。 [质量规则第 9 节](../../../docs/operations/agent-code-quality-rules.md#L267)

聚合清空按注入顺序独立发生，子收集器 panic 时的已执行状态需要与间歇读记录区分。 [metrics/multi.go:120](../../../metrics/multi.go#L120)

[复审报告](../../../docs/evidence/stable-quality-review-c3a77410.md)

## 验收与验证

- [x] 记录已清空和未调用者的状态、异常传播与后续重试语义。需要修改时单列契约。
- [x] 验收以记录观察结果和兼容性判断为准。

## 实施证据

三个子收集器都先记录一次，第二个 Clear 回调抛出已知值。恢复后累计为 0、1、1。撤销测试中的失败后重试，三个累计数均为 0。 临时探针通过，工作区内未新增对应实现。

```sh
go test -race -count=1 -v -overlay=/tmp/c3a-impl-analysis-overlay.json ./metrics -run '^TestReviewMultiClearPartialPanicAndRetry$'
```
