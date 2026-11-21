# Card 1801 — HEAD handler 异常退出时生成隐式成功状态

Milestone: 稳定模块质量复审
Recipe: specs/change-recipes/fix-bug.yaml
Context Package: implementation
Priority: P2
State: done
Primary Module: router
Owned Files: `router/dispatch.go`, `router/head_regression_test.go`
Depends On: 无

Goal: 对照 GET、HEAD、Goexit、普通空返回和显式状态，核对最终提交次数。
Scope: 记录下游是否正常返回。异常退出时按已有逻辑状态处理，避免制造隐式成功。
Non-goals: 维持稳定公开签名与标准库依赖，将验证限于本卡范围。
Files: `router/dispatch.go`, `router/head_regression_test.go`。
Tests: 对照 GET、HEAD、Goexit、普通空返回和显式状态，核对最终提交次数。
Docs Sync: 复核本卡涉及的已公开约定。
Done Definition: 完成下列验收，记录验证命令、状态及剩余限制。
Outcome: HEAD 的异常退出保留已经选定的状态，未写入响应且异常结束时不提交隐式 200。

## 证据与影响

第 9 节检查号为 23,24,37,46。证据状态为已复现。

未写头的同一 handler 主动结束 goroutine，GET 不提交，HEAD 外层 defer 提交一次 200。panic 的现有分支仅提交已有逻辑状态。 [router/dispatch.go:170](../../../router/dispatch.go#L170)

[复审报告](../../../docs/evidence/stable-quality-review-a074eadb.md)

## 验收与验证

- [x] 对照 GET、HEAD、Goexit、普通空返回和显式状态，核对最终提交次数。
- [x] 记录运行结果。若已有用例覆盖目标，记录用例及相关断言。

```sh
go test -race -timeout 60s ./router
```

## 实施证据

- [TestAcceptanceHeadGoexitDoesNotCommitImplicitSuccess](../../../router/head_regression_test.go#L444)
- [TestHeadPreservesCommitBeforeRecoveredPanic](../../../router/head_regression_test.go#L293)

[批次验证与基准](../review-a074eadb/README.md)
