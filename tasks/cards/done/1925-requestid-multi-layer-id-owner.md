# Card 1925 — 核对观测中间件与 requestid 的顺序和同一 ID

Milestone: 稳定模块质量复审
Recipe: specs/change-recipes/add-acceptance-tests.yaml
Context Package: implementation
Priority: P2
State: done
Primary Module: middleware
Owned Files: `middleware/conformance/response_writer_contract_test.go`
Depends On: 无

Goal: 请求 Context、响应头和观测记录归到同一选定标识，生成器调用次数可解释。
Scope: 使用合成 generator 计数、空请求标识和一条合法预置标识，组合 accesslog、指标和 requestid。
Non-goals: 维持稳定公开签名与标准库依赖，将验证限于本卡范围。
Files: `middleware/conformance/response_writer_contract_test.go`。
Tests: 请求 Context、响应头和观测记录归到同一选定标识，生成器调用次数可解释。
Docs Sync: 记录核验结论；只在现有契约文字需要澄清时另提文档变更。
Done Definition: 完成下列验收，记录验证命令、状态及剩余限制。
Outcome: 在显式生成器位于观察器内外时，所有观察层、请求和响应均共享实际选定的 ID。

## 证据与影响

第 9 节检查号为 17,25,27,39。证据状态为补证。

多个观测层共享生成路径。需把显式 generator 在外层与内层时的优先顺序固定在同一实际请求上。 [middleware/internal/telemetry/helpers.go:38](../../../middleware/internal/telemetry/helpers.go#L38)

[复审报告](../../../docs/evidence/stable-quality-review-2b94cf86.md)

## 验收与验证

- [x] 请求 Context、响应头和观测记录归到同一选定标识，生成器调用次数可解释。
- [x] 记录挂载入口、具体断言和结果。已有测试能覆盖时复用并注明。

```sh
go test -race -timeout 60s ./middleware/conformance
```

## 实施证据

- [TestLayeredObserversShareSelectedRequestIDAcrossOrder](../../../middleware/conformance/response_writer_contract_test.go#L553)

[批次验证与基准](../review-2b94cf86/README.md)
