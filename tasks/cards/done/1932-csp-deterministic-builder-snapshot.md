# Card 1932 — 核对 CSP Builder 多次 Build 的顺序和独立性

Milestone: 稳定模块质量复审
Recipe: specs/change-recipes/add-acceptance-tests.yaml
Context Package: implementation
Priority: P3
State: done
Primary Module: security
Owned Files: `security/headers/headers_test.go`
Depends On: 无

Goal: 顺序可重复，旧构建结果不随源数据变化。
Scope: 使用本地合成策略和独立数组，修改源数组及 builder 后比较两个已生成字符串。
Non-goals: 维持稳定公开签名与标准库依赖，将验证限于本卡范围。
Files: `security/headers/headers_test.go`。
Tests: 顺序可重复，旧构建结果不随源数据变化。
Docs Sync: 记录核验结论；只在现有契约文字需要澄清时另提文档变更。
Done Definition: 完成下列验收，记录验证命令、状态及剩余限制。
Outcome: CSP Build 的指令顺序可复验，修改传入源列表或 builder 不改变之前生成的值。

## 证据与影响

第 9 节检查号为 5,14,16,34,40。证据状态为补证。

构建结果按默认顺序拼接多个 directive，输入 slice 的变更不应影响稳定产物。 [security/headers/headers.go:599](../../../security/headers/headers.go#L599)

[复审报告](../../../docs/evidence/stable-quality-review-2b94cf86.md)

## 验收与验证

- [x] 顺序可重复，旧构建结果不随源数据变化。
- [x] 记录挂载入口、具体断言和结果。已有测试能覆盖时复用并注明。

```sh
go test -race -timeout 60s ./security/headers
```

## 实施证据

- [TestCSPBuildKeepsDirectiveOrderAndSourceSnapshot](../../../security/headers/headers_test.go#L749)

[批次验证与基准](../review-2b94cf86/README.md)
