# Card 1967 — 核对 205 响应不会调用用户序列化逻辑

Milestone: 稳定模块质量复审
Recipe: specs/change-recipes/add-acceptance-tests.yaml
Context Package: implementation
Priority: P2
State: done
Primary Module: contract
Owned Files: `contract/response_test.go`
Depends On: 无

Goal: 205 无响应体且 marshaler 调用为零，200 只调用一次。
Scope: 使用公开 WriteResponse 及 205、普通 200 对照。
Non-goals: 维持稳定公开签名与标准库依赖，将验证限于本卡范围。
Files: `contract/response_test.go`。
Tests: 205 无响应体且 marshaler 调用为零，200 只调用一次。
Docs Sync: 记录核验结论；只在现有契约文字需要澄清时另提文档变更。
Done Definition: 完成下列验收，记录验证命令、状态及剩余限制。
Outcome: 205 响应无正文并且不调用 marshaler，传输编码移除、长度置零。200 对照仅调用一次。

## 证据与影响

第 9 节检查号为 17,23,24,38，当前证据状态为补证。 [质量规则第 9 节](../../../docs/operations/agent-code-quality-rules.md#L267)

响应路径会区分允许与禁止响应体的状态。205 配合可计次 marshaler 可验证禁写分支的副作用边界。 [contract/response.go:22](../../../contract/response.go#L22)

[复审报告](../../../docs/evidence/stable-quality-review-999d5e4d.md)

## 验收与验证

- [x] 205 无响应体且 marshaler 调用为零，200 只调用一次。
- [x] 记录挂载入口、具体断言和结果。已有测试能覆盖时复用并注明。

```sh
go test -race -timeout 60s ./contract
```

## 实施证据

- [TestResetContentDoesNotCallUserMarshaler](../../../contract/response_test.go#L937)
