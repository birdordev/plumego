# Card 1841 — 核对活动链接发布失败与其后轮换的清理边界

Milestone: 稳定模块质量复审
Recipe: specs/change-recipes/fix-bug.yaml
Context Package: implementation
Priority: P2
State: done
Primary Module: log
Owned Files: `log/glog_test.go`, `log/glog_rotation.go`
Depends On: 无

Goal: 记录旧活动入口和新 writer 的可用性，失败后无临时目录。
Scope: 受控日志目录和替身文件组，组合失败发布与再次轮换。
Non-goals: 维持稳定公开签名与标准库依赖，将验证限于本卡范围。
Files: `log/glog_test.go`, `log/glog_rotation.go`。
Tests: 记录旧活动入口和新 writer 的可用性，失败后无临时目录。
Docs Sync: 同步日志模块活动链接的保留规则，与 1804 共用模块说明。
Done Definition: 完成下列验收，记录验证命令、状态及剩余限制。
Outcome: 链接更新失败时保留仍被已发布链接引用的文件。成功替换后链接可读且无暂存目录。

## 证据与影响

第 9 节检查号为 29,30,31,43。证据状态为补证。

轮换链接失败采用 best-effort，writer 已切换；继续执行归档清理时需确认目标文件所有权。 [log/glog_rotation.go:144](../../../log/glog_rotation.go#L144)

[复审报告](../../../docs/evidence/stable-quality-review-a074eadb.md)

## 验收与验证

- [x] 记录旧活动入口和新 writer 的可用性，失败后无临时目录。
- [x] 记录运行结果。若已有用例覆盖目标，记录用例及相关断言。

```sh
go test -race -timeout 60s ./log
```

## 实施证据

- [TestRetentionKeepsPublishedLinkTargetUntilReplacement](../../../log/glog_test.go#L1705)

[批次验证与基准](../review-a074eadb/README.md)
