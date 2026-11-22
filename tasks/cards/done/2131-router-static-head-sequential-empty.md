# Card 2131 — 核对非 seek 静态资源空文件的 GET 和 HEAD

Milestone: 稳定模块第 9 节复审
Recipe: specs/change-recipes/analysis-only.yaml
Context Package: review
Priority: P3
State: done
Primary Module: router
Owned Files: `router/static_test.go`
Depends On: 无

Goal: 核对非 seek 静态资源空文件的 GET 和 HEAD。
Scope: 复用静态 FS 替身，经本机服务器核对状态、显式长度、媒体类型和关闭次数。
Non-goals: 本卡提交取证结果。后续行为变更由独立修复卡执行。
Files: `router/static_test.go`
Tests: 对照空文件、非空文件和读取错误，核验 HEAD 正文为空以及 GET 无多余状态。
Docs Sync: 核对既有文档与验收结果一致，记录需要后续同步的位置。
Done Definition: 记录命令、结果、证据限制和是否需要独立修复卡。
Outcome: 本机 HTTP 对空的不可 seek JSON 文件执行 GET 与 HEAD，二者均为 200、长度 0 且媒体类型正确，HEAD 不读正文。现有用例覆盖非空、失败后关闭及读取失败。保留现行行为。

## 证据与影响

证据状态为待补证。第 9 节检查号为 6,17,22,24,29,37。已有 seek 路径的空文件回归和非 seek 正文回归，非 seek 且大小为零时真实服务器的头字段和资源关闭次数待核对。 [router/static.go:210](../../../router/static.go#L210) [router/static.go:271](../../../router/static.go#L271) [router/static_test.go:512](../../../router/static_test.go#L512) [router/static_test.go:632](../../../router/static_test.go#L632)

[复审报告](../../../docs/evidence/stable-quality-review-6e8d53f9.md)

## 验收与验证

- [x] 对照空文件、非空文件和读取错误，核验 HEAD 正文为空以及 GET 无多余状态。
- [x] 记录命令、结果、证据限制和是否需要独立修复卡。

```sh
go test -race -timeout 60s ./router
```
