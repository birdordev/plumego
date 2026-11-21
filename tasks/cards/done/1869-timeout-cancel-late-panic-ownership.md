# Card 1869 — 核对父取消后迟到 panic 的遥测与终态

Milestone: 稳定模块质量复审
Recipe: specs/change-recipes/add-acceptance-tests.yaml
Context Package: implementation
Priority: P2
State: done
Primary Module: middleware
Owned Files: `middleware/timeout/timeout_regression_test.go`
Depends On: 无

Goal: 取消响应归属明确，诊断一次且只含受控元数据，底层 writer 后续无写入。
Scope: 用通道确定取消已返回，再释放下游让其 panic，以公开 wrapper 记录 writer 和诊断。
Non-goals: 维持稳定公开签名与标准库依赖，将验证限于本卡范围。
Files: `middleware/timeout/timeout_regression_test.go`。
Tests: 取消响应归属明确，诊断一次且只含受控元数据，底层 writer 后续无写入。
Docs Sync: 记录核验结论；只在现有契约文字需要澄清时另提文档变更。
Done Definition: 完成下列验收，记录验证命令、状态及剩余限制。
Outcome: 父 Context 取消先决定响应归属。下游迟到 panic 仅触发一次既有诊断回调，不写底层。

## 证据与影响

第 9 节检查号为 23,26,29,33,39。证据状态为补证。

迟到的 panic 走独立诊断回调。父取消已选定响应后，应核对 panic 不再提交状态，回调只执行一次。 [middleware/timeout/timeout.go:204](../../../middleware/timeout/timeout.go#L204)

[复审报告](../../../docs/evidence/stable-quality-review-a8c2f28a.md)

## 验收与验证

- [x] 取消响应归属明确，诊断一次且只含受控元数据，底层 writer 后续无写入。
- [x] 记录挂载入口、具体断言和结果。已有测试能覆盖时复用并注明。

```sh
go test -race -timeout 60s ./middleware/timeout
```

## 实施证据

- [TestCanceledParentOwnsResponseBeforeLatePanicHook](../../../middleware/timeout/timeout_regression_test.go#L493)

[批次验证与基准](../review-a8c2f28a/README.md)
