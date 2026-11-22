# Card 2122 — 保留请求合并首个已提交响应的尾部字段

Milestone: 稳定模块第 9 节复审
Recipe: specs/change-recipes/fix-bug.yaml
Context Package: implementation
Priority: P2
State: done
Primary Module: middleware/singleflight
Owned Files: `middleware/singleflight/singleflight.go`, `middleware/singleflight/response_regression_test.go`
Depends On: 无

Goal: 保留请求合并首个已提交响应的尾部字段。
Scope: 把首个响应的 trailer 发布移到异常路径也运行的收尾。只发布已提交且没有接管连接的尾部值。保持 panic 原因与等候者的失败判定。
Non-goals: 保持公开签名、依赖和其余模块的行为。
Files: `middleware/singleflight/singleflight.go`, `middleware/singleflight/response_regression_test.go`
Tests: 覆盖正常返回、已提交后的 panic 和 Goexit、提交前异常、接管连接后退出。断言状态写一次，等候者无部分响应重放。
Docs Sync: 核对请求合并的异常与重放口径，涉及公开行为时同步现有说明。
Done Definition: 完成以下回归和验收，记录实际命令及结果。
Outcome: 已提交 trailer 的复制在首个请求退出时执行，状态 201 和尾部值在 panic 与 Goexit 路径均保留。非提交和接管路径继续保持原有所有权，原始 panic 透传。正常重放、异常退出和资源清理的包级竞态测试通过。

## 证据与影响

证据状态为已复现。第 9 节检查号为 17,23,24,29,39,45,46。首个请求写出 201 和正文，填入已声明的 trailer 后 panic。已提交状态和正文保留，尾部值只在正常返回时复制，恢复后尾部为空。 [middleware/singleflight/singleflight.go:308](../../../middleware/singleflight/singleflight.go#L308) [middleware/singleflight/singleflight.go:330](../../../middleware/singleflight/singleflight.go#L330) [middleware/singleflight/response_regression_test.go:386](../../../middleware/singleflight/response_regression_test.go#L386)

[复审报告](../../../docs/evidence/stable-quality-review-6e8d53f9.md)

## 验收与验证

- [x] 覆盖正常返回、已提交后的 panic 和 Goexit、提交前异常、接管连接后退出。断言状态写一次，等候者无部分响应重放。
- [x] 完成以下回归和验收，记录实际命令及结果。

```sh
go test -race -timeout 60s ./middleware/singleflight/...
```

实现时先运行 `make fmt`，按实际改动范围预览并运行 diff 门禁。
