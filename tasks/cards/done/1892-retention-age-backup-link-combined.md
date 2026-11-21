# Card 1892 — 联测年龄与备份数量同时设置时的活动保护

Milestone: 稳定模块质量复审
Recipe: specs/change-recipes/add-acceptance-tests.yaml
Context Package: implementation
Priority: P2
State: done
Primary Module: log
Owned Files: `log/glog_test.go`
Depends On: 无

Goal: 两个活动文件保留，一般过期归档及超额归档按各自规则清除。
Scope: 使用受控 mtime 的同程序测试目录，与旧链接目标和当前文件组合。
Non-goals: 维持稳定公开签名与标准库依赖，将验证限于本卡范围。
Files: `log/glog_test.go`。
Tests: 两个活动文件保留，一般过期归档及超额归档按各自规则清除。
Docs Sync: 记录核验结论；只在现有契约文字需要澄清时另提文档变更。
Done Definition: 完成下列验收，记录验证命令、状态及剩余限制。
Outcome: 同一清理同时约束年龄和备份数，当前 writer 与仍被活动链接引用的文件均保留。

## 证据与影响

第 9 节检查号为 14,29,31,43。证据状态为补证。

已发布链接和当前 writer 排除先于年龄过滤。年龄与备份数量同时启用时，需要验证两类清理各自的边界。 [log/glog_rotation.go:264](../../../log/glog_rotation.go#L264)

[复审报告](../../../docs/evidence/stable-quality-review-a8c2f28a.md)

## 验收与验证

- [x] 两个活动文件保留，一般过期归档及超额归档按各自规则清除。
- [x] 记录挂载入口、具体断言和结果。已有测试能覆盖时复用并注明。

```sh
go test -race -timeout 60s ./log
```

## 实施证据

- [TestRetentionUsesAgeAndBackupsAfterProtectingPublishedFiles](../../../log/glog_test.go#L1745)

[批次验证与基准](../review-a8c2f28a/README.md)
