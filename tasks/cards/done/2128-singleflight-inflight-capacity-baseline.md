# Card 2128 — 验证请求合并在途状态的资源上界

Milestone: 稳定模块第 9 节复审
Recipe: specs/change-recipes/analysis-only.yaml
Context Package: review
Priority: P2
State: done
Primary Module: middleware/singleflight
Owned Files: `middleware/singleflight/response_regression_test.go`
Depends On: 无

Goal: 验证请求合并在途状态的资源上界。
Scope: 用固定且有限的合成请求和受控 handler 测在途计数、释放和分配，确定资源上界应由哪一层承担。
Non-goals: 本卡提交取证结果。后续行为变更由独立修复卡执行。
Files: `middleware/singleflight/response_regression_test.go`
Tests: 先阻塞有限数量首个请求，核对等待方离开、首个请求释放和 Stats 收敛。记录上游并发队列的实际约束。
Docs Sync: 核对既有文档与验收结果一致，记录需要后续同步的位置。
Done Definition: 记录命令、结果、证据限制和是否需要独立修复卡。
Outcome: 有限在途基准以 1、8、32 个键测得约 7.5、53.9、216 KB/op，释放后 Stats 均归零。等待方退出、首个请求交接及队列行为由现有竞态测试验证；应用级并发限制负责跨键的请求上界。

## 证据与影响

证据状态为待补证。第 9 节检查号为 29,35,39,47,48。单响应有回放预算，等待方有等待时间。不同键的在途映射依赖首个请求终止才释放，整体峰值和上游并发限制的组合效果未量化。 [middleware/singleflight/singleflight.go:86](../../../middleware/singleflight/singleflight.go#L86) [middleware/singleflight/singleflight.go:136](../../../middleware/singleflight/singleflight.go#L136) [middleware/singleflight/singleflight.go:193](../../../middleware/singleflight/singleflight.go#L193) [middleware/singleflight/singleflight.go:356](../../../middleware/singleflight/singleflight.go#L356)

[复审报告](../../../docs/evidence/stable-quality-review-6e8d53f9.md)

## 验收与验证

用同名基准记录有限并发样本的峰值在途计数和每轮分配。

- [x] 先阻塞有限数量首个请求，核对等待方离开、首个请求释放和 Stats 收敛。记录上游并发队列的实际约束。
- [x] 记录命令、结果、证据限制和是否需要独立修复卡。

```sh
go test -race -timeout 60s ./middleware/singleflight
go test ./middleware/singleflight -run '^$' -bench '^BenchmarkCoalescerInFlightCapacity$' -benchmem -benchtime=200ms -count=3/...
```
