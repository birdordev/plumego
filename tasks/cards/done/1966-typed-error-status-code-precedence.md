# Card 1966 — 核对类型化错误对自定义状态与代码的优先级

Milestone: 稳定模块质量复审
Recipe: specs/change-recipes/add-acceptance-tests.yaml
Context Package: implementation
Priority: P2
State: done
Primary Module: contract
Owned Files: `contract/errors_chain_test.go`
Depends On: 无

Goal: HTTP 状态、code、type 与类别符合相同错误契约。
Scope: 用普通领域错误构建不同设置顺序，通过 AsAPIError 和 WriteError 对比。
Non-goals: 维持稳定公开签名与标准库依赖，将验证限于本卡范围。
Files: `contract/errors_chain_test.go`。
Tests: HTTP 状态、code、type 与类别符合相同错误契约。
Docs Sync: 记录核验结论；只在现有契约文字需要澄清时另提文档变更。
Done Definition: 完成下列验收，记录验证命令、状态及剩余限制。
Outcome: Type 会写入分类及默认 code，后设 Code 可以覆盖。AsAPIError 提取原值，WriteError 按已知类型归一化传输输出。

## 证据与影响

第 9 节检查号为 6,18,21,38，当前证据状态为补证。 [质量规则第 9 节](../../../docs/operations/agent-code-quality-rules.md#L267)

类型元数据、显式状态及显式代码参与归一化。需要记录已有调用方能依赖的优先级。 [contract/errors.go:430](../../../contract/errors.go#L430)

[复审报告](../../../docs/evidence/stable-quality-review-999d5e4d.md)

## 验收与验证

- [x] HTTP 状态、code、type 与类别符合相同错误契约。
- [x] 记录挂载入口、具体断言和结果。已有测试能覆盖时复用并注明。

```sh
go test -race -timeout 60s ./contract
```

## 实施证据

- [TestKnownTypeNormalizesStatusAndPreservesExplicitCode](../../../contract/errors_chain_test.go#L263)
