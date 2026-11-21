# Card 1809 — 核对关停在已取消和立即截止时的原因链

Milestone: 稳定模块质量复审
Recipe: specs/change-recipes/add-acceptance-tests.yaml
Context Package: implementation
Priority: P2
State: done
Primary Module: core
Owned Files: `core/lifecycle_test.go`
Depends On: 无

Goal: 关停保留对应标准原因，重试退出时连接释放。
Scope: 复用可控活动连接和诊断 logger，分别核对 errors.Is 及最终清理。
Non-goals: 维持稳定公开签名与标准库依赖，将验证限于本卡范围。
Files: `core/lifecycle_test.go`。
Tests: 关停保留对应标准原因，重试退出时连接释放。
Docs Sync: 复核本卡涉及的已公开约定。
Done Definition: 完成下列验收，记录验证命令、状态及剩余限制。
Outcome: 日志回调 panic 不替换关停原始的取消或截止原因。

## 证据与影响

第 9 节检查号为 22,26,36,46。证据状态为补证。

回调保护已有截止回归。需比较调用者取消和已逝期限两类 Context。 [core/lifecycle.go:95](../../../core/lifecycle.go#L95)

[复审报告](../../../docs/evidence/stable-quality-review-a074eadb.md)

## 验收与验证

- [x] 关停保留对应标准原因，重试退出时连接释放。
- [x] 记录运行结果。若已有用例覆盖目标，记录用例及相关断言。

```sh
go test -race -timeout 60s ./core
```

## 实施证据

- [TestAcceptanceShutdownPreservesErrorWhenLoggerPanics](../../../core/lifecycle_test.go#L1068)

[批次验证与基准](../review-a074eadb/README.md)
