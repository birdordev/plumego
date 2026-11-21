# Card 1849 — 压缩包装在 Goexit 路径隐式提交 200

Milestone: 稳定模块质量复审
Recipe: specs/change-recipes/fix-bug.yaml
Context Package: implementation
Priority: P2
State: done
Primary Module: middleware
Owned Files: `middleware/compression/gzip.go`, `middleware/compression/gzip_test.go`, `middleware/compression/README.md`
Depends On: 无

Goal: 本机 writer 对照 gzip、identity、正常空返回、panic、Goexit 和已开始的 gzip 流，分别核对提交次数、尾部和错误归属。
Scope: 显式记录下游是否正常返回。异常时沿用已有的未提交放弃、已提交资源收尾规则，保留 panic 传播。
Non-goals: 维持稳定公开签名与标准库依赖，将验证限于本卡范围。
Files: `middleware/compression/gzip.go`, `middleware/compression/gzip_test.go`, `middleware/compression/README.md`。
Tests: 本机 writer 对照 gzip、identity、正常空返回、panic、Goexit 和已开始的 gzip 流，分别核对提交次数、尾部和错误归属。
Docs Sync: 已公开行为变更时同步包说明。
Done Definition: 完成下列验收，记录验证命令、状态及剩余限制。
Outcome: 通过正常返回标记区分异常退出。gzip 处理器异常结束时放弃未提交状态，已开始的压缩流完整收尾，panic 仍传播。

## 证据与影响

第 9 节检查号为 20,23,24,39,47。证据状态为已复现。

handler 只修改 Header 后异常退出。gzip 分支提交一次 200，identity 分支没有提交。未选择状态的异常退出应让外层保留终态控制。 [middleware/compression/gzip.go:88](../../../middleware/compression/gzip.go#L88)

[复审报告](../../../docs/evidence/stable-quality-review-a8c2f28a.md)

## 验收与验证

- [x] 本机 writer 对照 gzip、identity、正常空返回、panic、Goexit 和已开始的 gzip 流，分别核对提交次数、尾部和错误归属。
- [x] 记录挂载入口、具体断言和结果。已有测试能覆盖时复用并注明。

```sh
go test -race -timeout 60s ./middleware/compression
```

## 实施证据

- [TestAcceptanceGzipAbnormalExitKeepsCommitOwnership](../../../middleware/compression/gzip_test.go#L1818)

[批次验证与基准](../review-a8c2f28a/README.md)
