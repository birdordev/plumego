# Card 1897 — 核对门面 Param 的 nil 请求和普通元数据

Milestone: 稳定模块质量复审
Recipe: specs/change-recipes/add-acceptance-tests.yaml
Context Package: implementation
Priority: P3
State: done
Primary Module: .
Owned Files: `plumego_test.go`
Depends On: 无

Goal: nil 安全且缺省为空，已有请求元数据按 owner 契约读取。
Scope: 使用同一公开 API 表验证 nil、普通请求和预装元数据的请求。
Non-goals: 维持稳定公开签名与标准库依赖，将验证限于本卡范围。
Files: `plumego_test.go`。
Tests: nil 安全且缺省为空，已有请求元数据按 owner 契约读取。
Docs Sync: 记录核验结论；只在现有契约文字需要澄清时另提文档变更。
Done Definition: 完成下列验收，记录验证命令、状态及剩余限制。
Outcome: 根门面 Param 接受 nil 请求，并透传普通元数据中的参数。

## 证据与影响

第 9 节检查号为 3,6,27,37。证据状态为补证。

门面对 router.Param 直接转发，已有路由与父同名覆盖验证。单独对照缺失请求、无参数和带 legacy 元数据的请求。 [plumego.go:73](../../../plumego.go#L73)

[复审报告](../../../docs/evidence/stable-quality-review-a8c2f28a.md)

## 验收与验证

- [x] nil 安全且缺省为空，已有请求元数据按 owner 契约读取。
- [x] 记录挂载入口、具体断言和结果。已有测试能覆盖时复用并注明。

```sh
go test -race -timeout 60s .
```

## 实施证据

- [TestFacadeParamAcceptsNilAndLegacyMetadata](../../../plumego_test.go#L209)

[批次验证与基准](../review-a8c2f28a/README.md)
