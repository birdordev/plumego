# Card 1904 — gzip 在底层不支持 Flush 时提前提交并关闭压缩选择

Milestone: 稳定模块质量复审
Recipe: specs/change-recipes/fix-bug.yaml
Context Package: implementation
Priority: P2
State: done
Primary Module: middleware
Owned Files: `middleware/compression/gzip.go`, `middleware/compression/gzip_test.go`, `middleware/compression/README.md`
Depends On: 无

Goal: 覆盖显式与隐式状态、Unwrap 链、不支持的底层、有 Flush 的真实 writer 和已开启 gzip 的流，确认 fallback 可继续编码且状态只提交一次。
Scope: 当响应未提交、尚未启用 gzip 且可确定底层缺少能力时，尽早返回不支持。保留真实 Flush 和只有动态错误可识别的 writer 的既有提交归属。
Non-goals: 维持稳定公开签名与标准库依赖，将验证限于本卡范围。
Files: `middleware/compression/gzip.go`, `middleware/compression/gzip_test.go`, `middleware/compression/README.md`。
Tests: 覆盖显式与隐式状态、Unwrap 链、不支持的底层、有 Flush 的真实 writer 和已开启 gzip 的流，确认 fallback 可继续编码且状态只提交一次。
Docs Sync: 已公开行为变更时同步包说明。
Done Definition: 完成下列验收，记录验证命令、状态及剩余限制。
Outcome: 已知底层完全不具备 Flush 时保留未提交状态与普通 gzip fallback。动态返回不支持与实际 Flush 延续原提交边界。

## 证据与影响

第 9 节检查号为 17,23,24,39,46。证据状态为已复现。

普通 writer 没有 Flush 能力。handler 声明 202 后试用 ResponseController.Flush，收到 ErrNotSupported，但底层已提交 202，随后正常写入被改成 identity。 [middleware/compression/gzip.go:481](../../../middleware/compression/gzip.go#L481)

[复审报告](../../../docs/evidence/stable-quality-review-2b94cf86.md)

## 验收与验证

- [x] 覆盖显式与隐式状态、Unwrap 链、不支持的底层、有 Flush 的真实 writer 和已开启 gzip 的流，确认 fallback 可继续编码且状态只提交一次。
- [x] 记录挂载入口、具体断言和结果。已有测试能覆盖时复用并注明。

```sh
go test -race -timeout 60s ./middleware/compression
```

## 实施证据

- [TestUnsupportedExplicitFlushAllowsNormalCompressionFallback](../../../middleware/compression/gzip_test.go#L1906)
- [TestDynamicFlushRefusalPreservesSelectedStatusCommit](../../../middleware/compression/gzip_test.go#L1945)
- [TestFlushAfterExplicitHeaderBeforeBody](../../../middleware/compression/gzip_test.go#L1584)
- [TestUnsupportedInitialFlushLeavesFinalResponseOpen](../../../middleware/compression/informational_regression_test.go#L17)

[批次验证与基准](../review-2b94cf86/README.md)
