# Card 1810 — 核对路由自省结果的独立所有权

Milestone: 稳定模块质量复审
Recipe: specs/change-recipes/add-acceptance-tests.yaml
Context Package: implementation
Priority: P3
State: done
Primary Module: core
Owned Files: `core/introspection_test.go`
Depends On: 无

Goal: 原有路由名和标签不受调用者修改。
Scope: 修改一份 Tags 返回数据，然后重读 App 和分派 metadata。
Non-goals: 维持稳定公开签名与标准库依赖，将验证限于本卡范围。
Files: `core/introspection_test.go`。
Tests: 原有路由名和标签不受调用者修改。
Docs Sync: 复核本卡涉及的已公开约定。
Done Definition: 完成下列验收，记录验证命令、状态及剩余限制。
Outcome: 路由枚举返回独立标签快照。

## 证据与影响

第 9 节检查号为 6,16,34,36。证据状态为补证。

App 路由自省把现有元数据向上暴露。已有 router 快照验证，应用转发边界需联测。 [core/app.go:70](../../../core/app.go#L70)

[复审报告](../../../docs/evidence/stable-quality-review-a074eadb.md)

## 验收与验证

- [x] 原有路由名和标签不受调用者修改。
- [x] 记录运行结果。若已有用例覆盖目标，记录用例及相关断言。

```sh
go test -race -timeout 60s ./core
```

## 实施证据

- [TestAppRouteIntrospectionReturnsIndependentTags](../../../core/introspection_test.go#L25)

[批次验证与基准](../review-a074eadb/README.md)
