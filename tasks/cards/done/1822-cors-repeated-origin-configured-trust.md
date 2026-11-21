# Card 1822 — 补证 CORS 决策的配置与响应边界

Milestone: 稳定模块质量复审
Recipe: specs/change-recipes/add-acceptance-tests.yaml
Context Package: implementation
Priority: P2
State: done
Primary Module: middleware
Owned Files: `middleware/cors/cors_test.go`
Depends On: 无

Goal: 预检和业务调用以允许列表为准，既有拒绝不污染下次决策。
Scope: 共用同一 middleware 实例完成两个不共享 Header 的本机请求。
Non-goals: 维持稳定公开签名与标准库依赖，将验证限于本卡范围。
Files: `middleware/cors/cors_test.go`。
Tests: 预检和业务调用以允许列表为准，既有拒绝不污染下次决策。
Docs Sync: 复核本卡涉及的已公开约定。
Done Definition: 完成下列验收，记录验证命令、状态及剩余限制。
Outcome: 交错来源请求不会复用先前请求的跨域决策，并更新 Vary。

## 证据与影响

第 9 节检查号为 14,18,20,39,46。证据状态为补证。

已有 Vary 与预检结果测试。使用普通同源/异源 fixture 核对一个拒绝后紧跟可用请求的隔离。 [middleware/cors/cors.go:134](../../../middleware/cors/cors.go#L134)

[复审报告](../../../docs/evidence/stable-quality-review-a074eadb.md)

## 验收与验证

- [x] 预检和业务调用以允许列表为准，既有拒绝不污染下次决策。
- [x] 记录运行结果。若已有用例覆盖目标，记录用例及相关断言。

```sh
go test -race -timeout 60s ./middleware/cors
```

## 实施证据

- [TestCORSAllowedOriginStillWorksAfterAnotherOrigin](../../../middleware/cors/cors_test.go#L670)

[批次验证与基准](../review-a074eadb/README.md)
