# Card 1820 — 核对超时 worker 异常退出与取消交叠

Milestone: 稳定模块质量复审
Recipe: specs/change-recipes/add-acceptance-tests.yaml
Context Package: implementation
Priority: P2
State: done
Primary Module: middleware
Owned Files: `middleware/timeout/timeout_regression_test.go`
Depends On: 无

Goal: 取消不制造第二终态，worker 资源释放。
Scope: 用合成时钟或通道让两个信号都可见。
Non-goals: 维持稳定公开签名与标准库依赖，将验证限于本卡范围。
Files: `middleware/timeout/timeout_regression_test.go`。
Tests: 取消不制造第二终态，worker 资源释放。
Docs Sync: 复核本卡涉及的已公开约定。
Done Definition: 完成下列验收，记录验证命令、状态及剩余限制。
Outcome: 复用已有回归覆盖下游 Goexit，含 Context 取消与未取消。

## 证据与影响

第 9 节检查号为 23,26,29,39,47。证据状态为补证。

已有单独 Goexit 和取消矩阵；当 worker 没有普通返回且父取消先可见时，需验证响应归属。 [middleware/timeout/timeout.go:162](../../../middleware/timeout/timeout.go#L162)

[复审报告](../../../docs/evidence/stable-quality-review-a074eadb.md)

## 验收与验证

- [x] 取消不制造第二终态，worker 资源释放。
- [x] 记录运行结果。若已有用例覆盖目标，记录用例及相关断言。

```sh
go test -race -timeout 60s ./middleware/timeout
```

## 实施证据

- [TestTimeoutDiscardsResponseWhenHandlerExits](../../../middleware/timeout/timeout_regression_test.go#L20)

[批次验证与基准](../review-a074eadb/README.md)
