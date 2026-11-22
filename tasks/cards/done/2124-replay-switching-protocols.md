# Card 2124 — 核对请求合并对 101 与接管连接的处理

Milestone: 稳定模块第 9 节复审
Recipe: specs/change-recipes/fix-bug.yaml
Context Package: implementation
Priority: P2
State: done
Primary Module: middleware/singleflight
Owned Files: `middleware/singleflight/singleflight.go`, `middleware/singleflight/response_regression_test.go`
Depends On: 无

Goal: 核对请求合并对 101 与接管连接的处理。
Scope: 用本机受控 writer 对照连接接管、普通 1xx 与最终状态。若可重放 101，最小修复是让这种响应只由首个请求持有。
Non-goals: 维护当前稳定签名和其他响应路径。
Files: `middleware/singleflight/singleflight.go`, `middleware/singleflight/response_regression_test.go`
Tests: 记录主动接管、明确 101、先 103 再 200 三条路径的首个响应与等候者结果。
Docs Sync: 核对既有文档与验收结果一致，记录需要后续同步的位置。
Done Definition: 记录命令、结果、证据限制和是否需要独立修复卡。
Outcome: 公开中间件的 101 结果现在标记为不可重放。受控请求分别得到首个 101 与等待方 502；103 后的 200 仍可正常重放；真实接管相关既有回归通过。随本轮直接修复。

## 证据与影响

证据状态为待补证。第 9 节检查号为 17,23,24,39,46。接管连接成功会显式标记不可重放，但写入 101 的单独路径只保存状态。标准服务器与包装器组合下的合法触发和等候者结果待验证。 [middleware/singleflight/singleflight.go:445](../../../middleware/singleflight/singleflight.go#L445) [middleware/singleflight/singleflight.go:518](../../../middleware/singleflight/singleflight.go#L518) [middleware/singleflight/singleflight.go:345](../../../middleware/singleflight/singleflight.go#L345)

[复审报告](../../../docs/evidence/stable-quality-review-6e8d53f9.md)

## 验收与验证

- [x] 记录主动接管、明确 101、先 103 再 200 三条路径的首个响应与等候者结果。
- [x] 记录命令、结果、证据限制和是否需要独立修复卡。

```sh
go test -race -timeout 60s ./middleware/singleflight/...
```
