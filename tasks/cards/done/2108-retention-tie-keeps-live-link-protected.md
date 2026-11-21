# Card 2108 — 同时间归档清理仍保护活跃链接与多级别

Milestone: 稳定模块第 9 节复审
Recipe: specs/change-recipes/add-acceptance-tests.yaml
Context Package: implementation
Priority: P2
State: done
Primary Module: log
Owned Files: `log/glog_test.go`
Depends On: 2065

Goal: 同时间归档清理仍保护活跃链接与多级别
Scope: 用 INFO 和 WARN 各两个同时间归档，再为每级发布活跃链接。
Non-goals: 范围限于本卡涉及的契约，保持公开签名和依赖。
Files: `log/glog_test.go`。
Tests: 依赖 2065，每级保留一份独立备份及活动链接，链接目标未被清理。
Docs Sync: 核对新增验收与已有模块说明一致，记录证据和限制。
Done Definition: 完成以下验收，记录命令、结果与剩余限制。
Outcome: 相同时间的 INFO 与 WARN 各保留更新的独立归档，两级活动链接目标都不占用备份次数。

## 证据与影响

证据状态为补证。第 9 节检查号为 28,29,35,43,45。 [质量规则第 9 节](../../../docs/operations/agent-code-quality-rules.md#L267)

归档排序变更后需明确活跃文件不占用备用数量，各级仍独立计算。 [log/glog_rotation.go:252](../../../log/glog_rotation.go#L252)

[复审报告](../../../docs/evidence/stable-quality-review-2f962d21.md)

## 验收与验证

- [x] 依赖 2065，每级保留一份独立备份及活动链接，链接目标未被清理。
- [x] 已有测试覆盖相同触发和断言时可直接复用，记录具体用例。

```sh
go test -race -timeout 60s ./log
```

## 实施证据

- [TestRetentionUsesGenerationToBreakModificationTies](../../../log/glog_test.go#L1905)
