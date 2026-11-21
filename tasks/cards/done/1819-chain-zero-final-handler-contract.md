# Card 1819 — 核对 Chain 对 nil 最终 handler 的构建约定

Milestone: 稳定模块质量复审
Recipe: specs/change-recipes/add-acceptance-tests.yaml
Context Package: implementation
Priority: P2
State: done
Primary Module: middleware
Owned Files: `middleware/middleware_test.go`
Depends On: 无

Goal: 记录可用与无效组合，构建期失败可预测。
Scope: 对照 nil 接口、nil HandlerFunc、零 builder 与短路 builder。
Non-goals: 维持稳定公开签名与标准库依赖，将验证限于本卡范围。
Files: `middleware/middleware_test.go`。
Tests: 记录可用与无效组合，构建期失败可预测。
Docs Sync: 复核本卡涉及的已公开约定。
Done Definition: 完成下列验收，记录验证命令、状态及剩余限制。
Outcome: 空链和使用空终端的短路编排保持现有返回语义。

## 证据与影响

第 9 节检查号为 6,15,17,39。证据状态为补证。

Build 校验每个 builder 和其返回值，零 builder 时直接返回末端接口。是否允许 builder 用空末端构造替代入口需保留原兼容。 [middleware/middleware.go:129](../../../middleware/middleware.go#L129)

[复审报告](../../../docs/evidence/stable-quality-review-a074eadb.md)

## 验收与验证

- [x] 记录可用与无效组合，构建期失败可预测。
- [x] 记录运行结果。若已有用例覆盖目标，记录用例及相关断言。

```sh
go test -race -timeout 60s ./middleware
```

## 实施证据

- [TestChainEmptyAndShortCircuitBuildersWithNilTerminal](../../../middleware/middleware_test.go#L206)

[批次验证与基准](../review-a074eadb/README.md)
