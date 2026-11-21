# Card 1900 — 探测异常后健康报告仍返回已过时的健康状态

Milestone: 稳定模块质量复审
Recipe: specs/change-recipes/fix-bug.yaml
Context Package: implementation
Priority: P1
State: done
Primary Module: x/observability/ops
Owned Files: `x/observability/ops/healthhttp/manager.go`, `x/observability/ops/healthhttp/metrics_test.go`, `x/observability/ops/healthhttp/aggregate_regression_test.go`, `docs/modules/x/observability/ops/README.md`
Depends On: 无

Goal: 本机覆盖异常退出、正常恢复、注销与替换、重叠组件检查和手工就绪。普通未执行任务不制造新样本，最新失败报告与聚合状态一致。
Scope: 在注册身份和检查版本都匹配时，把已尝试但异常退出的固定失败终态发布到组件缓存。保留最后实际探测的语义，未尝试项以及被替换的旧项不写入。
Non-goals: 维持稳定公开签名与标准库依赖，将验证限于本卡范围。
Files: `x/observability/ops/healthhttp/manager.go`, `x/observability/ops/healthhttp/metrics_test.go`, `x/observability/ops/healthhttp/aggregate_regression_test.go`, `docs/modules/x/observability/ops/README.md`。
Tests: 本机覆盖异常退出、正常恢复、注销与替换、重叠组件检查和手工就绪。普通未执行任务不制造新样本，最新失败报告与聚合状态一致。
Docs Sync: 行为变更后同步清单对应的模块说明并校验生成数据。
Done Definition: 完成下列验收，记录验证命令、状态及剩余限制。
Outcome: 异常中止的实际检查按注册身份和检查版本发布固定失败，保存已执行重试次数。较新检查、注销、替换与手工决定各保留其发布权，健康报告随有效结果更新。

## 证据与影响

第 9 节检查号为 22,28,31,32,42,46。证据状态为已复现。

组件先得到 healthy 快照，下一批 checker 提前退出。聚合返回 unhealthy，就绪变为 false，但同一 Tracker 的报告仍读取旧组件快照并返回 HTTP 200 和 healthy。报表消费者可能继续按健康处理。 [x/observability/ops/healthhttp/manager.go:266](../../../x/observability/ops/healthhttp/manager.go#L266)

[复审报告](../../../docs/evidence/stable-quality-review-2b94cf86.md)

## 验收与验证

- [x] 本机覆盖异常退出、正常恢复、注销与替换、重叠组件检查和手工就绪。普通未执行任务不制造新样本，最新失败报告与聚合状态一致。
- [x] 记录挂载入口、具体断言和结果。已有测试能覆盖时复用并注明。

```sh
go test -race -timeout 60s ./x/observability/ops/healthhttp
make website-sync
make website-check-generated
```

## 实施证据

- [TestAbortedBatchPublishesFailureForHealthReport](../../../x/observability/ops/healthhttp/metrics_test.go#L323)
- [TestAbortedComponentCannotReplaceNewerRegistrationOrCheck](../../../x/observability/ops/healthhttp/aggregate_regression_test.go#L165)

[批次验证与基准](../review-2b94cf86/README.md)
