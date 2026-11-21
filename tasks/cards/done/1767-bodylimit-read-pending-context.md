# Card 1767 — 核对读到超限后上下文取消的优先级

Milestone: 稳定模块质量复审
Recipe: specs/change-recipes/add-acceptance-tests.yaml
Context Package: implementation
Priority: P2
State: done
Primary Module: middleware
Owned Files: `middleware/bodylimit/body_limit_test.go`
Depends On: 无

Goal: 取消后无 413 追加，原有已提交状态保持。
Scope: 用控制通道在 reader 出错后取消，再让处理函数返回。
Non-goals: 维持稳定公开签名与标准库依赖，将验证限于本卡范围。
Files: `middleware/bodylimit/body_limit_test.go`。
Tests: 取消后无 413 追加，原有已提交状态保持。
Docs Sync: 复核本卡涉及的已公开约定。
Done Definition: 完成下列验收，记录验证命令、状态及剩余限制。
Outcome: 正文读取发现超限后，上下文在提交前取消会跳过待处理错误，原先已选状态和空正文保持。

## 证据与影响

第 9 节检查号为 23,26,29,39。证据状态为补证。

reader 只发布超限，writer 提交前再次看 context。需要确定取消发生在二者之间时的终态。 [middleware/bodylimit/body_limit.go:145](../../../middleware/bodylimit/body_limit.go#L145)

[复审报告](../../../docs/evidence/stable-quality-review-d224acbc.md)

## 验收与验证

- [x] 取消后无 413 追加，原有已提交状态保持。
- [x] 记录运行结果。若已有用例覆盖目标，记录用例及相关断言。

```sh
go test -race -timeout 60s ./middleware/bodylimit
```

## 实施证据

- [TestCancellationAfterReadOverrunSkipsPendingError](../../../middleware/bodylimit/body_limit_test.go#L807)

[批次验证与基准](../review-d224acbc/README.md)
