# Card 2134 — 核对超时缓冲的 Content-Length 写入语义

Milestone: 稳定模块第 9 节复审
Recipe: specs/change-recipes/fix-bug.yaml
Context Package: implementation
Priority: P2
State: done
Primary Module: middleware/timeout
Owned Files: `middleware/timeout/timeout.go`, `middleware/timeout/timeout_regression_test.go`
Depends On: 无

Goal: 核对超时缓冲的 Content-Length 写入语义。
Scope: 通过本机标准 writer 对照声明长度内外的合法写入与越界错误，据此决定是否需要在缓冲层保持净写入错误语义。
Non-goals: 维护当前稳定签名和其他响应路径。
Files: `middleware/timeout/timeout.go`, `middleware/timeout/timeout_regression_test.go`
Tests: 核对写一段、分段、显式零长度和超限后的最终状态，区分缓冲限制与网络写失败。
Docs Sync: 核对既有文档与验收结果一致，记录需要后续同步的位置。
Done Definition: 记录命令、结果、证据限制和是否需要独立修复卡。
Outcome: 本机 HTTP 对照复现超过声明长度时 handler 收到 nil。缓冲 writer 按已提交的头字段和累计写入量返回 http.ErrContentLength。分段、显式零长度及后置头修改均有永久回归。

## 证据与影响

证据状态为待补证。第 9 节检查号为 17,22,23,24,31,39,46。超时缓冲有正文类型与容量限制，未见按已声明长度拒绝单次写入的逻辑。真实服务器下 handler 写入结果与客户端结果待对照。 [middleware/timeout/timeout.go:241](../../../middleware/timeout/timeout.go#L241) [middleware/timeout/timeout.go:279](../../../middleware/timeout/timeout.go#L279) [middleware/timeout/timeout_test.go:366](../../../middleware/timeout/timeout_test.go#L366)

[复审报告](../../../docs/evidence/stable-quality-review-6e8d53f9.md)

## 验收与验证

- [x] 核对写一段、分段、显式零长度和超限后的最终状态，区分缓冲限制与网络写失败。
- [x] 记录命令、结果、证据限制和是否需要独立修复卡。

```sh
go test -race -timeout 60s ./middleware/timeout/...
```
