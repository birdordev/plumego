# Card 1927 — 核对 Debug 已转发的 Trailer 在异常退出时的响应归属

Milestone: 稳定模块质量复审
Recipe: specs/change-recipes/analysis-only.yaml
Context Package: review
Priority: P2
State: done
Primary Module: middleware
Owned Files: `middleware/debug/debug_errors_test.go`
Depends On: 无

Goal: 明确哪些字段已提交及底层收尾责任，不在异常后制造额外终态。
Scope: 场景发送普通头体并 Flush，然后设本机尾字段，模拟正常、panic 与异常终止。
Non-goals: 维持稳定公开签名与标准库依赖，将验证限于本卡范围。
Files: `middleware/debug/debug_errors_test.go`。
Tests: 明确哪些字段已提交及底层收尾责任，不在异常后制造额外终态。
Docs Sync: 记录核验结论；只在现有契约文字需要澄清时另提文档变更。
Done Definition: 完成下列验收，记录验证命令、状态及剩余限制。
Outcome: 临时探针确认 202 与正文已提交。正常返回复制晚期尾字段；panic 或 Goexit 不复制尚未发布的尾字段。发布权仍属于 writer，无额外提交。

## 证据与影响

第 9 节检查号为 23,24,29,39。证据状态为补证。

终态收尾在 next 正常返回后安装。需要先确定已经开始 passthrough 后异常退出时哪些 trailer 已发布。 [middleware/debug/debug_errors.go:125](../../../middleware/debug/debug_errors.go#L125)

[复审报告](../../../docs/evidence/stable-quality-review-2b94cf86.md)

## 验收与验证

- [x] 明确哪些字段已提交及底层收尾责任，不在异常后制造额外终态。
- [x] 记录挂载入口、具体断言和结果。已有测试能覆盖时复用并注明。

## 实施证据

本机 recorder 比较下游正常返回、panic 与 Goexit。先声明尾字段并 Flush，之后设置尾值。已选择的状态与正文在三种路径都可见，仅正常返回会将尚未发布的尾字段交给底层。

```sh
go test -race -timeout 40s -v /tmp/2b94-analysis-boundaries_test.go -run '^TestDebugPassthroughTrailerOwnership$'
```

[批次验证与基准](../review-2b94cf86/README.md)
