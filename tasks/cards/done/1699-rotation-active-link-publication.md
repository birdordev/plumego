# Card 1699 — 内部日志轮换失败时可能丢失原活动链接

Milestone: 稳定模块质量复审
Recipe: specs/change-recipes/fix-bug.yaml
Context Package: implementation
Priority: P2
State: done
Primary Module: log
Owned Files: `log/glog_rotation.go`, `log/glog_test.go`
Depends On: 无

Goal:

内部日志轮换失败时可能丢失原活动链接。

Scope:

在同目录创建临时链接并在成功后替换，失败时保留旧的活动链接，明确 Windows 替换行为。

Non-goals:

本卡范围限于所列文件。维持公开签名和现有依赖，兼容契约调整先核对调用边界。

Files:

- `log/glog_rotation.go`
- `log/glog_test.go`

## Impact and Ownership

主模块为 `log`。根模块继续使用标准库。拟编辑范围与运行影响见本卡证据及验收。涉及安全与生命周期的变化按公开入口核验。

## 证据

检查号为 29,30,31,43，状态为源码确认。

旧活动链接先移除，接着创建新链接。创建失败时文件 writer 已切换但链接失效；此处障碍限于内部文件后端。 [log/glog_rotation.go:150](https://github.com/spcent/plumego/blob/9430574179dc6b793524992833ce27496a5fe548/log/glog_rotation.go#L150)

[复审报告](../../../docs/evidence/stable-quality-review-94305741.md)

## Acceptance Tests

- `log/glog_test.go` 中的 `TestAcceptanceLogLinkReplacementKeepsOldTargetOnFailure`
- `log/glog_test.go` 中的 `TestRotationKeepsNewWriterWhenOldCloseFails`

验证新文件继续写入、旧链接在注入失败时可读、没有遗留临时链接。

Tests:

通过所列模块命令运行相邻用例并核对现有约定。

Docs Sync:

对照原契约核验，若证据要求修订公开行为，先更新实施范围。

## Validation

```sh
go test -race -timeout 60s ./log
```

Done Definition:

- [x] 验证新文件继续写入、旧链接在注入失败时可读、没有遗留临时链接。
- [x] 执行验证命令，记录状态、首次失败和剩余核验范围。

Outcome:

同一目录所在的文件系统先准备临时链接再发布，准备失败时原链接仍可读，清理临时目录。文件 writer 切换独立运行，其他平台的 rename 语义由操作系统提供。

## 实施证据

- [TestAcceptanceLogLinkReplacementKeepsOldTargetOnFailure](../../../log/glog_test.go#L1532)
- [TestRotationKeepsNewWriterWhenOldCloseFails](../../../log/glog_test.go#L1564)

[批次验证与基准](../review-94305741/README.md)
