# Card 1790 — 核对相同修改时间的归档文件保留策略

Milestone: 稳定模块质量复审
Recipe: specs/change-recipes/add-acceptance-tests.yaml
Context Package: implementation
Priority: P2
State: done
Primary Module: log
Owned Files: `log/glog_test.go`
Depends On: 无

Goal: 活动文件不受影响，同级保留数量准确，必要时记录平局策略。
Scope: 建立相同时间及多个级别的普通文件，核对已有活动文件。
Non-goals: 维持稳定公开签名与标准库依赖，将验证限于本卡范围。
Files: `log/glog_test.go`。
Tests: 活动文件不受影响，同级保留数量准确，必要时记录平局策略。
Docs Sync: 复核本卡涉及的已公开约定。
Done Definition: 完成下列验收，记录验证命令、状态及剩余限制。
Outcome: 相同修改时间下，活动文件保留，各等级留存数量正确。同时间归档顺序保持未指定。

## 证据与影响

第 9 节检查号为 6,31,43。证据状态为补证。

保留策略按修改时间排序，相同时间时无次序约定。文件系统时间粒度与程序文件名顺序需对照。 [log/glog_rotation.go:274](../../../log/glog_rotation.go#L274)

[复审报告](../../../docs/evidence/stable-quality-review-d224acbc.md)

## 验收与验证

- [x] 活动文件不受影响，同级保留数量准确，必要时记录平局策略。
- [x] 记录运行结果。若已有用例覆盖目标，记录用例及相关断言。

```sh
go test -race -timeout 60s ./log
```

## 实施证据

- [TestRetentionAtEqualModificationTimesKeepsQuotaAndActiveFiles](../../../log/glog_test.go#L1629)

[批次验证与基准](../review-d224acbc/README.md)
