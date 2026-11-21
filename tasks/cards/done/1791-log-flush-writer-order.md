# Card 1791 — 核对并发 Flush 与输出的同步边界

Milestone: 稳定模块质量复审
Recipe: specs/change-recipes/add-acceptance-tests.yaml
Context Package: implementation
Priority: P2
State: done
Primary Module: log
Owned Files: `log/glog_test.go`
Depends On: 无

Goal: 已完成的写入可读，Flush 和 Close 的原有退出语义清楚。
Scope: 通过可控写入确定 Sync 是否能在写返回前完成，记录调用契约。
Non-goals: 维持稳定公开签名与标准库依赖，将验证限于本卡范围。
Files: `log/glog_test.go`。
Tests: 已完成的写入可读，Flush 和 Close 的原有退出语义清楚。
Docs Sync: 复核本卡涉及的已公开约定。
Done Definition: 完成下列验收，记录验证命令、状态及剩余限制。
Outcome: 镜像输出仍在阻塞写入时，文件 Flush 已可完成。确认落盘数据，现有关闭并发回归保留。

## 证据与影响

第 9 节检查号为 29,31,32,43。证据状态为补证。

Flush 在读锁调用 Sync，输出有独立写序列，当前并发用例主要验证无崩溃。 [log/glog_lifecycle.go:3](../../../log/glog_lifecycle.go#L3)

[复审报告](../../../docs/evidence/stable-quality-review-d224acbc.md)

## 验收与验证

- [x] 已完成的写入可读，Flush 和 Close 的原有退出语义清楚。
- [x] 记录运行结果。若已有用例覆盖目标，记录用例及相关断言。

```sh
go test -race -timeout 60s ./log
```

## 实施证据

- [TestFileFlushCompletesWhileMirrorIsStillWriting](../../../log/glog_test.go#L1664)
- [TestFileLoggerFlushCloseAndWritesComplete](../../../log/glog_test.go#L1504)

[批次验证与基准](../review-d224acbc/README.md)
