# Card 1859 — 补证 DefaultConfig 的重复调用与依赖选择

Milestone: 稳定模块质量复审
Recipe: specs/change-recipes/add-acceptance-tests.yaml
Context Package: implementation
Priority: P3
State: done
Primary Module: core
Owned Files: `core/core_public_test.go`
Depends On: 无

Goal: 保留默认值及两个 logger 的隔离，诊断到对应依赖。
Scope: 用两份配置及两个内存输出组合，改变一份超时或地址，触发一条可追踪诊断。
Non-goals: 维持稳定公开签名与标准库依赖，将验证限于本卡范围。
Files: `core/core_public_test.go`。
Tests: 保留默认值及两个 logger 的隔离，诊断到对应依赖。
Docs Sync: 记录核验结论；只在现有契约文字需要澄清时另提文档变更。
Done Definition: 完成下列验收，记录验证命令、状态及剩余限制。
Outcome: 两次默认配置和显式 logger 均保持每个 App 独立。

## 证据与影响

第 9 节检查号为 3,5,6,14,36。证据状态为补证。

公开配置按值返回，实际 logger 另由显式依赖选择。需验证独立构造在准备前后的默认值与诊断去向。 [core/config.go:39](../../../core/config.go#L39)

[复审报告](../../../docs/evidence/stable-quality-review-a8c2f28a.md)

## 验收与验证

- [x] 保留默认值及两个 logger 的隔离，诊断到对应依赖。
- [x] 记录挂载入口、具体断言和结果。已有测试能覆盖时复用并注明。

```sh
go test -race -timeout 60s ./core
```

## 实施证据

- [TestDefaultConfigAndExplicitLoggersStayInstanceLocal](../../../core/core_public_test.go#L734)

[批次验证与基准](../review-a8c2f28a/README.md)
