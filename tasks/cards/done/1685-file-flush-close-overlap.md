# Card 1685 — 验证文本内部文件 Flush 与 Close 的锁序

Milestone: 稳定模块质量复审
Recipe: specs/change-recipes/add-acceptance-tests.yaml
Context Package: implementation
Priority: P2
State: done
Primary Module: log
Owned Files: `log/glog_test.go`
Depends On: 无

Goal: 验证文本内部文件 Flush 与 Close 的锁序。
Scope: 添加通道控制的并发写和文件锁顺序观察，记录实际 Close 边界。
Non-goals: 维持公开签名和依赖；涉及现有兼容契约的调整须先核对调用边界及迁移影响。
Files: `log/glog_test.go`。
Tests: 无悬挂，结果文件可重新读取，内部临时资源退出。
Docs Sync: 对照原契约核验，若证据要求修订公开行为，先更新实施范围。
Done Definition: 完成测试与验收记录，并标明剩余取证范围。
Outcome: 实际文件写入、Flush 和 Close 并发完成，文件可重新读，文件句柄元数据被释放。

## 证据

检查号为 28,29,30,32,43，状态为补证。

Flush 持有读锁跨多次 Sync，Close 同时序列化写与状态。 [log/glog_lifecycle.go:3](../../../log/glog_lifecycle.go#L3)

[复审报告](../../../docs/evidence/stable-quality-review-09a7f5d3.md)

## 验收与验证

- [x] 无悬挂，结果文件可重新读取，内部临时资源退出。
- [x] 记录验证命令、状态和剩余范围。若使用既有用例满足验收，记录对应证据。

```sh
go test -race -timeout 60s ./log
```

## 实施证据

[TestFileLoggerFlushCloseAndWritesComplete](../../../log/glog_test.go#L1504)
