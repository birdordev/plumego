# Card 2084 — 压缩 103 后的 304 保留响应协议

Milestone: 稳定模块第 9 节复审
Recipe: specs/change-recipes/add-acceptance-tests.yaml
Context Package: implementation
Priority: P2
State: done
Primary Module: middleware/compression
Owned Files: `middleware/compression/informational_regression_test.go`
Depends On: 无

Goal: 压缩 103 后的 304 保留响应协议
Scope: 用回环和 httptrace 记录 103，再读取带验证头的 304。
Non-goals: 范围限于本卡涉及的契约，保持公开签名和依赖。
Files: `middleware/compression/informational_regression_test.go`。
Tests: 信息状态一次，304 头和零正文符合原生响应，未开始 gzip 流。
Docs Sync: 核对新增验收与已有模块说明一致，记录证据和限制。
Done Definition: 完成以下验收，记录命令、结果与剩余限制。
Outcome: 回环 103 正常发出，后续 304 保留 ETag，正文为空且没有启动 gzip 流。

## 证据与影响

证据状态为补证。第 9 节检查号为 23,24,39,45。 [质量规则第 9 节](../../../docs/operations/agent-code-quality-rules.md#L267)

压缩包装器区分信息与最终状态。检验立即接 304 且应用保留验证头。 [middleware/compression/gzip.go:232](../../../middleware/compression/gzip.go#L232)

[复审报告](../../../docs/evidence/stable-quality-review-2f962d21.md)

## 验收与验证

- [x] 信息状态一次，304 头和零正文符合原生响应，未开始 gzip 流。
- [x] 已有测试覆盖相同触发和断言时可直接复用，记录具体用例。

```sh
go test -race -timeout 60s ./middleware/compression
```

## 实施证据

- [TestGzipEarlyHintsThenNotModifiedOnLoopback](../../../middleware/compression/informational_regression_test.go#L462)
