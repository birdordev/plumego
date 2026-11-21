# Card 1961 — 核对 Use 中 nil 槽位的处理与冻结边界

Milestone: 稳定模块质量复审
Recipe: specs/change-recipes/add-acceptance-tests.yaml
Context Package: implementation
Priority: P3
State: done
Primary Module: core
Owned Files: `core/middleware_test.go`
Depends On: 无

Goal: 有效中间件按注册顺序执行，冻结尝试无副作用。
Scope: 对比初始化阶段混合输入和准备后的同一输入，记录执行轨迹。
Non-goals: 维持稳定公开签名与标准库依赖，将验证限于本卡范围。
Files: `core/middleware_test.go`。
Tests: 有效中间件按注册顺序执行，冻结尝试无副作用。
Docs Sync: 记录核验结论；只在现有契约文字需要澄清时另提文档变更。
Done Definition: 完成下列验收，记录验证命令、状态及剩余限制。
Outcome: Use 在准备前整批拒绝含 nil 的输入，后续有效项按顺序执行。Prepare 后拒绝修改。

## 证据与影响

第 9 节检查号为 5,28,36,46，当前证据状态为补证。 [质量规则第 9 节](../../../docs/operations/agent-code-quality-rules.md#L267)

Use 跳过 nil 与准备后冻结由不同分支负责。交错传入 nil 与有效中间件时应验证现行顺序。 [core/middleware.go:13](../../../core/middleware.go#L13)

[复审报告](../../../docs/evidence/stable-quality-review-999d5e4d.md)

## 验收与验证

- [x] 有效中间件按注册顺序执行，冻结尝试无副作用。
- [x] 记录挂载入口、具体断言和结果。已有测试能覆盖时复用并注明。

```sh
go test -race -timeout 60s ./core
```

## 实施证据

- [TestUseNilValidationIsAtomicBeforeAndAfterFreeze](../../../core/middleware_test.go#L29)
