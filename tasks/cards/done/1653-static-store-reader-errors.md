# Card 1653 — 记录顺序文件复制中断的最终状态

Milestone: 稳定模块质量复审
Recipe: specs/change-recipes/add-acceptance-tests.yaml
Context Package: implementation
Priority: P2
State: done
Primary Module: router
Owned Files: `router/static_test.go`
Depends On: 无

Goal: 记录顺序文件复制中断的最终状态。
Scope: 用测试 file 注入短读，分开 pre-commit 与 post-commit 观察。
Non-goals: 维持公开签名和依赖；涉及现有兼容契约的调整须先核对调用边界及迁移影响。
Files: `router/static_test.go`。
Tests: 记录终态、是否截断及 Close 次数，已提交后无追加错误正文。
Docs Sync: 对照原契约核验，若证据要求修订公开行为，先更新实施范围。
Done Definition: 完成测试与验收记录，并标明剩余取证范围。
Outcome: 受控顺序文件在读出任何字节前或输出部分后报错，观察截断与一次关闭，没有附加错误正文。

## 证据

检查号为 22,23,24,29,37，状态为补证。

顺序读取可能在复制前或复制中返回 I/O 错误，当前 handler 只关闭文件并丢弃 copy 错误。 [router/static.go:290](../../../router/static.go#L290)

[复审报告](../../../docs/evidence/stable-quality-review-09a7f5d3.md)

## 验收与验证

- [x] 记录终态、是否截断及 Close 次数，已提交后无追加错误正文。
- [x] 记录验证命令、状态和剩余范围。若使用既有用例满足验收，记录对应证据。

```sh
go test -race -timeout 60s ./router
```

## 实施证据

[TestStaticSequentialReadErrorDoesNotAppendAnotherResponse](../../../router/static_test.go#L612)
