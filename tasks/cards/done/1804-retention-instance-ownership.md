# Card 1804 — 共享日志目录的保留策略缺少其他实例的活动文件信息

Milestone: 稳定模块质量复审
Recipe: specs/change-recipes/analysis-only.yaml
Context Package: review
Priority: P2
State: done
Primary Module: log
Owned Files: `log/glog_rotation.go`, `log/glog_test.go`, `docs/modules/log/README.md`
Depends On: 无

Goal: 以合成文件组验证无共同文件名前缀的程序归档互不影响、已发布文件受保护，且本程序的备份配额继续生效。
Scope: 先明确目录所有权和兼容范围；需支持共享时，按可验证的实例归属隔离活动文件和清理对象。
Non-goals: 维持稳定公开签名与标准库依赖，将验证限于本卡范围。
Files: `log/glog_rotation.go`, `log/glog_test.go`, `docs/modules/log/README.md`。
Tests: 以合成文件组验证无共同文件名前缀的程序归档互不影响、已发布文件受保护，且本程序的备份配额继续生效。
Docs Sync: 复核模块说明并验证生成数据。
Done Definition: 完成下列验收，记录验证命令、状态及剩余限制。
Outcome: 文档规定同时运行的同名实例各用独立目录。验证无共同文件名前缀的程序文件组和已发布目标的保护，以及本程序备份配额。同名目录仍按程序名汇总历史归档，未加入跨进程协调。

## 证据与影响

第 9 节检查号为 16,29,31,43。证据状态为源码确认。

排除的活动文件仅来自本实例，候选按程序名、等级和时间归组。另一同名实例共用目录时，其文件可进入归档删除候选。未验证多进程部署。 [log/glog_rotation.go:250](../../../log/glog_rotation.go#L250)

[复审报告](../../../docs/evidence/stable-quality-review-a074eadb.md)

## 验收与验证

- [x] 以合成文件组验证无共同文件名前缀的程序归档互不影响、已发布文件受保护，且本程序的备份配额继续生效。
- [x] 记录运行结果。若已有用例覆盖目标，记录用例及相关断言。

```sh
go test -race -timeout 60s ./log
make website-sync
make website-check-generated
```

## 实施证据

- [TestLogRetentionGroupsBackupsByFilenameLevel](../../../log/glog_test.go#L1259)
- [TestRetentionKeepsPublishedLinkTargetUntilReplacement](../../../log/glog_test.go#L1705)

[批次验证与基准](../review-a074eadb/README.md)
