# Card 1587 — 在真实包装链验证 HEAD 控制器

Milestone: 本轮质量审查
Recipe: specs/change-recipes/add-acceptance-tests.yaml
Context Package: implementation
Priority: P2
State: done
Primary Module: router
Owned Files: `router/head_regression_test.go`
Depends On: 无

Goal: 在真实包装链验证 HEAD 控制器。
Scope: 加最小两层 Unwrap 夹具，区分已选状态与尚未选状态。
Non-goals: 保留现有公开签名、依赖与模块边界。测试若证实新的缺陷，先记录证据再单独安排修复。
Files: `router/head_regression_test.go`。
Tests: 校验 Flush 结果、写入次数、无正文及失败后可完成响应。
Docs Sync: 本卡按现有契约验证。若取证表明契约需调整，先更新后续卡范围。
Done Definition: 完成下列验收和验证，记录证据与剩余风险。
Outcome: 真实 HTTP HEAD 通过两层 Unwrap 转发 Flush，保留最终头且零正文。已有测试覆盖不支持 Flush 后回退。

## 证据与影响

- 检查号：17,23,24,37。证据状态：补证。
- HEAD 已有单层 Flush/劫持回归，组合 ResponseController 和真实传输的边界仍需核对。 [router/dispatch.go:116](../../../router/dispatch.go#L116)
- [审查报告](../../../docs/evidence/stable-quality-review-dee7aa0f.md)

## 验收与验证

- [x] 校验 Flush 结果、写入次数、无正文及失败后可完成响应。
- [x] 运行以下命令并记录结果、失败路径和未验证范围。修改 Go 文件前完成仓库预检，验证前先执行仓库格式化。

```sh
go test -race -timeout 60s ./router
```

## 实施证据

[TestHeadControllerThroughWrappedServer](../../../router/head_regression_test.go#L320) · [TestHeadUnsupportedFlushLeavesResponseOpen](../../../router/head_regression_test.go#L22)
