# Card 2055 — 轮换新文件名能参与既有保留策略

Milestone: 稳定模块第 9 节复审
Recipe: specs/change-recipes/add-acceptance-tests.yaml
Context Package: implementation
Priority: P2
State: done
Primary Module: log
Owned Files: `log/glog_test.go`
Depends On: 2009

Goal: 轮换新文件名能参与既有保留策略
Scope: 建两代名字和活动文件，控制 modtime 与保留数量。
Non-goals: 范围限于本卡涉及的契约，保持公开签名和依赖。
Files: `log/glog_test.go`。
Tests: 过期离线归档可清理，活动文件与其链接仍存在。
Docs Sync: 核对新增验收与已有模块说明一致，记录证据和限制。
Done Definition: 完成以下验收，记录命令、结果与剩余限制。
Outcome: 新候选仍使用原命名格式，旧式归档参与保留淘汰，新活动文件和链接存续。

## 证据与影响

证据状态为补证。第 9 节检查号为 6,29,35,43,45。 [质量规则第 9 节](../../../docs/operations/agent-code-quality-rules.md#L267)

归档保留依赖文件名解析与活动链接保护。若 2009 引入新候选命名，应核验旧式文件与新式文件一起处理。 [log/glog_rotation.go:303](../../../log/glog_rotation.go#L303)

[复审报告](../../../docs/evidence/stable-quality-review-c3a77410.md)

## 验收与验证

- [x] 过期离线归档可清理，活动文件与其链接仍存在。
- [x] 已有测试覆盖相同触发和断言时可直接复用，记录具体用例。

```sh
go test -race -timeout 60s ./log
```

## 实施证据

- [TestRotationRetentionRecognizesCollisionArchiveAndLegacyFile](../../../log/glog_test.go#L1858)
