# Card 1873 — 核对信息响应及外层观察器只记录一次最终 500

Milestone: 稳定模块质量复审
Recipe: specs/change-recipes/add-acceptance-tests.yaml
Context Package: implementation
Priority: P2
State: done
Primary Module: middleware
Owned Files: `middleware/conformance/response_writer_contract_test.go`
Depends On: 无

Goal: 信息头不影响唯一最终状态和唯一观察，异常时记录 500，正常时记录 204。
Scope: 以本机 writer 或 loopback 驱动 metrics 加 recovery，比较 103 后异常与正常 204。
Non-goals: 维持稳定公开签名与标准库依赖，将验证限于本卡范围。
Files: `middleware/conformance/response_writer_contract_test.go`。
Tests: 信息头不影响唯一最终状态和唯一观察，异常时记录 500，正常时记录 204。
Docs Sync: 记录核验结论；只在现有契约文字需要澄清时另提文档变更。
Done Definition: 完成下列验收，记录验证命令、状态及剩余限制。
Outcome: 对照信息头后正常 204 和恢复后的 500，外层 HTTP 指标均只观察一次最终状态。

## 证据与影响

第 9 节检查号为 20,23,24,25,39。证据状态为补证。

已有直接 recovery 的 103 后 500 回归。组合外围包裹的 HTTP 指标观察器，需确认先有信息头时采样的是恢复提交的最终状态。 [middleware/recovery/recover.go:69](../../../middleware/recovery/recover.go#L69)

[复审报告](../../../docs/evidence/stable-quality-review-a8c2f28a.md)

## 验收与验证

- [x] 信息头不影响唯一最终状态和唯一观察，异常时记录 500，正常时记录 204。
- [x] 记录挂载入口、具体断言和结果。已有测试能覆盖时复用并注明。

```sh
go test -race -timeout 60s ./middleware/conformance
```

## 实施证据

- [TestObservedFinalStatusAfterEarlyHintsAndRecovery](../../../middleware/conformance/response_writer_contract_test.go#L517)

[批次验证与基准](../review-a8c2f28a/README.md)
