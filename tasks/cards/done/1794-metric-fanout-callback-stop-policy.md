# Card 1794 — 核对子收集器回调失败的传播范围

Milestone: 稳定模块质量复审
Recipe: specs/change-recipes/analysis-only.yaml
Context Package: review
Priority: P2
State: done
Primary Module: metrics
Owned Files: `metrics/multi_test.go`
Depends On: 无

Goal: 按既有传播约定排序，不在验证卡变更回调策略。
Scope: 用人工记录器产生受控 panic，在外部捕获并记录前后子状态。
Non-goals: 维持稳定公开签名与标准库依赖，将验证限于本卡范围。
Files: `metrics/multi_test.go`。
Tests: 按既有传播约定排序，不在验证卡变更回调策略。
Docs Sync: 复核本卡涉及的已公开约定。
Done Definition: 完成下列验收，记录验证命令、状态及剩余限制。
Outcome: 同步 fan-out 保留原回调错误边界。第二个子收集器 panic 后停止向后传播，原 panic 可由调用层捕获。

## 证据与影响

第 9 节检查号为 23,33,44,46。证据状态为补证。

聚合器同步调用各子收集器。第一项回调失效时是否继续由公开契约与消费方决定。 [metrics/multi.go:58](../../../metrics/multi.go#L58)

[复审报告](../../../docs/evidence/stable-quality-review-d224acbc.md)

## 验收与验证

- [x] 按既有传播约定排序，不在验证卡变更回调策略。
- [x] 记录运行结果。若已有用例覆盖目标，记录用例及相关断言。

```sh
go test -race -timeout 60s ./metrics
```

## 实施证据

- [TestAggregateStopsPropagationAtPanickingRecordCollector](../../../metrics/multi_test.go#L521)

[批次验证与基准](../review-d224acbc/README.md)
