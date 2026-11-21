# Card 2075 — 普通无扩展名静态内容的 MIME 与 HEAD

Milestone: 稳定模块第 9 节复审
Recipe: specs/change-recipes/add-acceptance-tests.yaml
Context Package: implementation
Priority: P2
State: done
Primary Module: router
Owned Files: `router/static_test.go`
Depends On: 无

Goal: 普通无扩展名静态内容的 MIME 与 HEAD
Scope: 以实际文件目录设置无扩展名纯文本，回环对比 GET、HEAD 回退及重复请求。
Non-goals: 范围限于本卡涉及的契约，保持公开签名和依赖。
Files: `router/static_test.go`。
Tests: 类型、长度与状态一致，HEAD 体为空。
Docs Sync: 核对新增验收与已有模块说明一致，记录证据和限制。
Done Definition: 完成以下验收，记录命令、结果与剩余限制。
Outcome: 真实文件及回环 HTTP 验证无扩展名文本的 MIME、长度及空 HEAD，重复 HEAD 保持结果。

## 证据与影响

证据状态为补证。第 9 节检查号为 17,24,37,45。 [质量规则第 9 节](../../../docs/operations/agent-code-quality-rules.md#L267)

标准文件走 ServeContent 嗅探，HEAD 的包装器负责快照和长度。 [router/static.go:271](../../../router/static.go#L271)

[复审报告](../../../docs/evidence/stable-quality-review-2f962d21.md)

## 验收与验证

- [x] 类型、长度与状态一致，HEAD 体为空。
- [x] 已有测试覆盖相同触发和断言时可直接复用，记录具体用例。

```sh
go test -race -timeout 60s ./router
```

## 实施证据

- [TestExtensionlessStaticFileSniffsContentTypeForHeadAndGet](../../../router/static_test.go#L829)
