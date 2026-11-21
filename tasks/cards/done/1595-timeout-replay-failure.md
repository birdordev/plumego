# Card 1595 — 补证超时缓冲回放写失败的提交权

Milestone: 本轮质量审查
Recipe: specs/change-recipes/add-acceptance-tests.yaml
Context Package: implementation
Priority: P2
State: done
Primary Module: middleware
Owned Files: `middleware/timeout/timeout_regression_test.go`
Depends On: 无

Goal: 补证超时缓冲回放写失败的提交权。
Scope: 注入只允许单次写入的响应 writer。
Non-goals: 保留现有公开签名、依赖与模块边界。测试若证实新的缺陷，先记录证据再单独安排修复。
Files: `middleware/timeout/timeout_regression_test.go`。
Tests: 记录最终状态次数、写调用次数、外层收到的数据；写失败不触发第二个错误响应。
Docs Sync: 本卡按现有契约验证。若取证表明契约需调整，先更新后续卡范围。
Done Definition: 完成下列验收和验证，记录证据与剩余风险。
Outcome: 回放 writer 返回错误时只尝试一次正文，保持已提交的 201。

## 证据与影响

- 检查号：21,23,24,39,46。证据状态：补证。
- 成功 handler 的回放写入由外层完成，回放写错误被丢弃。需锁定错误之后没有再次提交。 [middleware/timeout/timeout.go:275](../../../middleware/timeout/timeout.go#L275)
- [审查报告](../../../docs/evidence/stable-quality-review-dee7aa0f.md)

## 验收与验证

- [x] 记录最终状态次数、写调用次数、外层收到的数据；写失败不触发第二个错误响应。
- [x] 运行以下命令并记录结果、失败路径和未验证范围。修改 Go 文件前完成仓库预检，验证前先执行仓库格式化。

```sh
go test -race -timeout 60s ./middleware/timeout
```

## 实施证据

[TestTimeoutReplayWriteFailureDoesNotCommitAgain](../../../middleware/timeout/timeout_regression_test.go#L396)
