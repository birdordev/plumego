# Card 1934 — 核对普通 Header 名值检查的空值与多字节边界

Milestone: 稳定模块质量复审
Recipe: specs/change-recipes/add-acceptance-tests.yaml
Context Package: implementation
Priority: P2
State: done
Primary Module: security
Owned Files: `security/input/input_test.go`
Depends On: 无

Goal: 输入分类可重复，合法字段保持；不改变代理或网络处理。
Scope: 以几组正常不敏感字面量验证单函数与 HTTP Policy 入口各自拥有的行为。
Non-goals: 维持稳定公开签名与标准库依赖，将验证限于本卡范围。
Files: `security/input/input_test.go`。
Tests: 输入分类可重复，合法字段保持；不改变代理或网络处理。
Docs Sync: 记录核验结论；只在现有契约文字需要澄清时另提文档变更。
Done Definition: 完成下列验收，记录验证命令、状态及剩余限制。
Outcome: 同一组无害输入显示 Header 名和值的合法字节规则不同，空值、普通空格和 Unicode 的行为已固定。

## 证据与影响

第 9 节检查号为 6,18,22,40。证据状态为补证。

名称和值使用不同字节约束。策略编译消费两者，需用普通合成配置标明空值、空格和非 ASCII 何时保留。 [security/input/input.go:120](../../../security/input/input.go#L120)

[复审报告](../../../docs/evidence/stable-quality-review-2b94cf86.md)

## 验收与验证

- [x] 输入分类可重复，合法字段保持；不改变代理或网络处理。
- [x] 记录挂载入口、具体断言和结果。已有测试能覆盖时复用并注明。

```sh
go test -race -timeout 60s ./security/input
```

## 实施证据

- [TestHeaderNameAndValueHaveSeparateByteContracts](../../../security/input/input_test.go#L560)

[批次验证与基准](../review-2b94cf86/README.md)
