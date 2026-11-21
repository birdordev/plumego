# Card 1633 — HEAD 被拒绝的 Flush 提前结束长度推断

Milestone: 稳定模块质量复审
Recipe: specs/change-recipes/fix-bug.yaml
Context Package: implementation
Priority: P2
State: done
Primary Module: router
Owned Files: `router/dispatch.go`, `router/head_regression_test.go`, `docs/modules/router/README.md`, `website/src/generated/modules.ts`
Depends On: 无

Goal: HEAD 被拒绝的 Flush 提前结束长度推断。
Scope: 区分确认支持的 flush 与拒绝，保留无实际 flush 时的长度和嗅探能力。先明确能力探测与发送已选最终状态的次序。
Non-goals: 维持公开签名和依赖；涉及现有兼容契约的调整须先核对调用边界及迁移影响。
Files: `router/dispatch.go`, `router/head_regression_test.go`, `docs/modules/router/README.md`, `website/src/generated/modules.ts`。
Tests: 覆盖显式状态、ErrNotSupported、传输故障和成功 flush 四条路径；最终状态和正文规则维持原样。
Docs Sync: 复核 `docs/modules/router/README.md`，模块说明改动后核对生成文件差异。
Done Definition: 完成测试与验收记录，并标明剩余取证范围。
Outcome: 无 Flush 能力时保留长度推断。对已选定状态且提供 FlushError 的 writer，保留先发最终状态再刷新，并记录运行时拒绝无法撤回提交的限制。

## 证据

检查号为 6,23,24,37，状态为已复现。

最终状态已逻辑选定后，实际 writer 返回 ErrNotSupported，路由层仍先 commit(false)。同一三字节响应不尝试 flush 时头中有长度 3，尝试被拒绝后没有长度。 [router/dispatch.go:130](../../../router/dispatch.go#L130)

[复审报告](../../../docs/evidence/stable-quality-review-09a7f5d3.md)

## 验收与验证

- [x] 覆盖显式状态、ErrNotSupported、传输故障和成功 flush 四条路径；最终状态和正文规则维持原样。
- [x] 记录验证命令、状态和剩余范围。若使用既有用例满足验收，记录对应证据。

```sh
go test -race -timeout 60s ./router
make website-sync
make website-check-generated
```

## 实施证据

[TestHeadSelectedStatusFlushCommitBoundary](../../../router/head_regression_test.go#L355) · [TestHeadUnsupportedFlushLeavesResponseOpen](../../../router/head_regression_test.go#L22)
