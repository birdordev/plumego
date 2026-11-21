# Card 1959 — 核对嵌套生命周期组的就绪错误身份

Milestone: 稳定模块质量复审
Recipe: specs/change-recipes/add-acceptance-tests.yaml
Context Package: implementation
Priority: P2
State: done
Primary Module: core
Owned Files: `core/lifecycle_group_test.go`
Depends On: 无

Goal: 记录错误链、组件名称和实际调用顺序，重复调用结果可解释。
Scope: 装配内外两组和可计次组件，分别返回普通错误和上下文结束。
Non-goals: 维持稳定公开签名与标准库依赖，将验证限于本卡范围。
Files: `core/lifecycle_group_test.go`。
Tests: 记录错误链、组件名称和实际调用顺序，重复调用结果可解释。
Docs Sync: 记录核验结论；只在现有契约文字需要澄清时另提文档变更。
Done Definition: 完成下列验收，记录验证命令、状态及剩余限制。
Outcome: 正常就绪轮询保留内外组名称及子错误，取消时合并 context.Canceled 并跳过余下回调。

## 证据与影响

第 9 节检查号为 11,22,26,36，当前证据状态为补证。 [质量规则第 9 节](../../../docs/operations/agent-code-quality-rules.md#L267)

Ready 检查已准备组并按注册顺序读取子组件。嵌套组遇到子错误时，错误链和后继 Ready 调用范围仍需联测。 [core/lifecycle_group.go:275](../../../core/lifecycle_group.go#L275)

[复审报告](../../../docs/evidence/stable-quality-review-999d5e4d.md)

## 验收与验证

- [x] 记录错误链、组件名称和实际调用顺序，重复调用结果可解释。
- [x] 记录挂载入口、具体断言和结果。已有测试能覆盖时复用并注明。

```sh
go test -race -timeout 60s ./core
```

## 实施证据

- [TestNestedReadyPreservesErrorAndProbeOrder](../../../core/lifecycle_group_test.go#L853)
