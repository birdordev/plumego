# Card 1747 — 初始化文件日志先删除活动链接

Milestone: 稳定模块质量复审
Recipe: specs/change-recipes/fix-bug.yaml
Context Package: implementation
Priority: P2
State: done
Primary Module: log
Owned Files: `log/glog_rotation.go`, `log/glog_test.go`
Depends On: 无

Goal: 验证旧链接、临时目录、部分初始化和重试；原生平台语义按平台验证。
Scope: 首选复用 replaceLogSymlink；单独核对分级初始化失败后已发布链接指向仍可读取的文件。
Non-goals: 维持稳定公开签名与标准库依赖，将验证限于本卡范围。
Files: `log/glog_rotation.go`, `log/glog_test.go`。
Tests: 验证旧链接、临时目录、部分初始化和重试；原生平台语义按平台验证。
Docs Sync: 复核本卡涉及的已公开约定。
Done Definition: 完成下列验收，记录验证命令、状态及剩余限制。
Outcome: 初始化复用可保留目标的先准备后替换。较晚级别失败时已发布目标可读，文件资源回收，移除失败条件后重试可写且无暂存目录。

## 证据与影响

第 9 节检查号为 29,30,31,43。证据状态为源码确认。

初始化逐级删除旧链接再创建，生成失败时已打开的 writer 会清理，旧链接仍可能丢失。轮换已有准备后替换的实现。 [log/glog_rotation.go:66](../../../log/glog_rotation.go#L66)

[复审报告](../../../docs/evidence/stable-quality-review-d224acbc.md)

## 验收与验证

- [x] 验证旧链接、临时目录、部分初始化和重试；原生平台语义按平台验证。
- [x] 记录运行结果。若已有用例覆盖目标，记录用例及相关断言。

```sh
go test -race -timeout 60s ./log
```

## 实施证据

- [TestAcceptanceInitLinkFailurePreservesTargetsAndCanRetry](../../../log/glog_test.go#L1586)
- [TestInitLogFilesCleansPartialFilesOnSymlinkFailure](../../../log/glog_test.go#L929)

[批次验证与基准](../review-d224acbc/README.md)
