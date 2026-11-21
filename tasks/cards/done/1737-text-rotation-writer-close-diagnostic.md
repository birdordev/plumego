# Card 1737 — 核对内部轮换丢失旧句柄关闭错误的影响

Milestone: 稳定模块质量复审
Recipe: specs/change-recipes/add-acceptance-tests.yaml
Context Package: implementation
Priority: P2
State: done
Primary Module: log
Owned Files: `log/glog_test.go`
Depends On: 无

Goal:

核对内部轮换丢失旧句柄关闭错误的影响。

Scope:

先核对可传播错误与新 writer 的保留，再用合成错误夹具补证。

Non-goals:

本卡范围限于所列文件。维持公开签名和现有依赖，兼容契约调整先核对调用边界。

Files:

- `log/glog_test.go`

## Impact and Ownership

主模块为 `log`。根模块继续使用标准库。候审目标是复核可观察契约并记录证据。

## 证据

检查号为 22,29,30,43，状态为补证。

已提交新 writer 后忽略旧关闭错误，原退出路径可报告轮换失败但不能回滚已发布 writer。 [log/glog_rotation.go:142](https://github.com/spcent/plumego/blob/9430574179dc6b793524992833ce27496a5fe548/log/glog_rotation.go#L142)

[复审报告](../../../docs/evidence/stable-quality-review-94305741.md)

## Acceptance Tests

- `log/glog_test.go` 中的 `TestRotationKeepsNewWriterWhenOldCloseFails`

既有输出不中断，错误只产生固定诊断或被有据省略。

Tests:

通过所列模块命令运行相邻用例并核对现有约定。

Docs Sync:

对照原契约核验，若证据要求修订公开行为，先更新实施范围。

## Validation

```sh
go test -race -timeout 60s ./log
```

Done Definition:

- [x] 既有输出不中断，错误只产生固定诊断或被有据省略。
- [x] 执行验证命令，记录状态、首次失败和剩余核验范围。

Outcome:

已关闭的旧文件句柄触发关闭错误后，新文件 writer 仍工作。旧句柄关闭保留 best-effort 语义，当前轮换成功无需回滚。

## 实施证据

- [TestRotationKeepsNewWriterWhenOldCloseFails](../../../log/glog_test.go#L1564)

[批次验证与基准](../review-94305741/README.md)
