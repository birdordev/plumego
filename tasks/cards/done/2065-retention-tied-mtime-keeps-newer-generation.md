# Card 2065 — 归档修改时间并列时保留较早世代

Milestone: 稳定模块第 9 节复审
Recipe: specs/change-recipes/fix-bug.yaml
Context Package: implementation
Priority: P2
State: done
Primary Module: log
Owned Files: `log/glog_rotation.go`, `log/glog_test.go`, `log/coverage_test.go`, `docs/modules/log/README.md`
Depends On: 无

Goal: 归档修改时间并列时保留较早世代
Scope: 保持修改时间为主排序键。同值时为同系列已识别的文件名定义后续世代优先规则，并明确不同系列或历史命名的确定性兜底。
Non-goals: 范围限于本卡涉及的契约，保持公开签名和依赖。
Files: `log/glog_rotation.go`, `log/glog_test.go`, `log/coverage_test.go`, `docs/modules/log/README.md`。
Tests: 同修改时间两代以上留下预期世代。追加不同时刻与原活动候选样例，分别验证主排序及活动保护继续有效。
Docs Sync: 已同步[所属模块说明](../../../docs/modules/log/README.md)，网站生成数据核对通过。
Done Definition: 完成以下验收，记录命令、结果与剩余限制。
Outcome: 完成。先按磁盘修改时间排序，同值比较归档名时间戳，最后按文件名决定顺序。现代及历史命名均参与保留。

## 证据与影响

证据状态为已复现。第 9 节检查号为 14,29,35,43,45。 [质量规则第 9 节](../../../docs/operations/agent-code-quality-rules.md#L267)

同一程序、主机、级别有三份轮换文件，文件名的分秒和纳秒可区分世代，磁盘修改时间被设为同值。保留一份时留下最早命名世代，后两份都被清理。真实文件系统发生频率未取证。 [log/glog_rotation.go:296](../../../log/glog_rotation.go#L296)

[复审报告](../../../docs/evidence/stable-quality-review-2f962d21.md)

## 验收与验证

- [x] 同修改时间两代以上留下预期世代。追加不同时刻与原活动候选样例，分别验证主排序及活动保护继续有效。
- [x] 已有测试覆盖相同触发和断言时可直接复用，记录具体用例。

```sh
go test -race -timeout 60s ./log
```

## 实施证据

- [TestRetentionUsesGenerationToBreakModificationTies](../../../log/glog_test.go#L1905)

同一回归在 2f962d21 的原实现上失败，修复后通过。
