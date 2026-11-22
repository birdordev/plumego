# Card 2135 — 核对 gzip 包裹路由时 HEAD 的表示元数据

Milestone: 稳定模块第 9 节复审
Recipe: specs/change-recipes/fix-bug.yaml
Context Package: implementation
Priority: P2
State: done
Primary Module: middleware/compression
Owned Files: `middleware/compression/gzip.go`, `middleware/compression/gzip_test.go`
Depends On: 2119

Goal: 核对 gzip 包裹路由时 HEAD 的表示元数据。
Scope: 固定同一路由和 Accept-Encoding，经真实本机 HTTP 对照 HEAD 与 GET。结合确认的表示元数据权属选择最小方案。
Non-goals: 维护当前稳定签名和其他响应路径。
Files: `middleware/compression/gzip.go`, `middleware/compression/gzip_test.go`
Tests: 核对 GET 压缩实体可解码，HEAD 无正文，Content-Length 和编码声明适用该表示，Vary 可供缓存区分。
Docs Sync: 核对既有文档与验收结果一致，记录需要后续同步的位置。
Done Definition: 记录命令、结果、证据限制和是否需要独立修复卡。
Outcome: 本机组装曾返回 GET gzip 长度 35、HEAD identity 长度 280。HEAD 无压缩实体时省略可能属于不同表示的长度与强校验头，保持 Vary、正文为空和不可压缩响应的元数据。永久回归与本机 HTTP 对照通过。

## 证据与影响

证据状态为待补证。第 9 节检查号为 6,17,23,24,37,39,45。router 抑制 HEAD 正文并能推断内容长度，gzip 依赖正文触发压缩决策。两层叠加后 GET、HEAD 的长度、编码及 Vary 组合待核对。 [middleware/compression/gzip.go:195](../../../middleware/compression/gzip.go#L195) [router/dispatch.go:80](../../../router/dispatch.go#L80) [router/dispatch.go:158](../../../router/dispatch.go#L158) [middleware/compression/gzip_test.go:662](../../../middleware/compression/gzip_test.go#L662)

[复审报告](../../../docs/evidence/stable-quality-review-6e8d53f9.md)

## 验收与验证

- [x] 核对 GET 压缩实体可解码，HEAD 无正文，Content-Length 和编码声明适用该表示，Vary 可供缓存区分。
- [x] 记录命令、结果、证据限制和是否需要独立修复卡。

```sh
go test -race -timeout 60s ./middleware/compression/...
```
