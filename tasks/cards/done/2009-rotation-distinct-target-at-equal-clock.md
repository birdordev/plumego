# Card 2009 — 固定时刻下轮换复用活动文件

Milestone: 稳定模块第 9 节复审
Recipe: specs/change-recipes/fix-bug.yaml
Context Package: implementation
Priority: P2
State: done
Primary Module: log
Owned Files: `log/glog_rotation.go`, `log/glog_test.go`, `docs/modules/log/README.md`
Depends On: 无

Goal: 固定时刻下轮换复用活动文件
Scope: 轮换仅在成功创建独立新文件后切换 writer 并清零，遇同名选用可被保留逻辑识别的候选名称。兼容既有文件名与链接更新次序。
Non-goals: 范围限于本卡涉及的契约，保持公开签名和依赖。
Files: `log/glog_rotation.go`, `log/glog_test.go`, `docs/modules/log/README.md`。
Tests: 同一固定时刻连续越阈值，验证活动路径单独切换、已归档内容完整且新活动文件计量准确。
Docs Sync: 已同步[所属模块说明](../../../docs/modules/log/README.md)，网站生成数据核对通过。
Done Definition: 完成以下验收，记录命令、结果与剩余限制。
Outcome: 已修复。新候选独占创建成功后切换活动 writer 和计量，两个同一时刻的写入分别形成完整归档。

## 证据与影响

证据状态为已复现。第 9 节检查号为 14,28,29,31,35,43,45。 [质量规则第 9 节](../../../docs/operations/agent-code-quality-rules.md#L267)

在固定时钟下初始化同一个私有文件 logger，再通过 Info 写入两条各 1 MiB 的记录。活动路径从未切换，文件已有两条记录，计量却归零。真实时钟碰撞频率未测。 [log/glog_rotation.go:143](../../../log/glog_rotation.go#L143)

[复审报告](../../../docs/evidence/stable-quality-review-c3a77410.md)

## 验收与验证

- [x] 同一固定时刻连续越阈值，验证活动路径单独切换、已归档内容完整且新活动文件计量准确。
- [x] 已有测试覆盖相同触发和断言时可直接复用，记录具体用例。

```sh
go test -race -timeout 60s ./log
```

## 实施证据

- [TestRotationWithFixedClockCreatesSeparateArchives](../../../log/glog_test.go#L1826)

同一回归在 c3a77410 的原实现上失败，修复后通过。
