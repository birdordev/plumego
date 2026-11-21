# Card 1761 — 验证响应 request_id 与错误自带 ID 的优先级

Milestone: 稳定模块质量复审
Recipe: specs/change-recipes/add-acceptance-tests.yaml
Context Package: implementation
Priority: P2
State: done
Primary Module: contract
Owned Files: `contract/response_test.go`
Depends On: 无

Goal: 响应始终使用规定优先级，未序列化 cause。
Scope: 通过 WriteError 对照合法、空和无效 ID。
Non-goals: 维持稳定公开签名与标准库依赖，将验证限于本卡范围。
Files: `contract/response_test.go`。
Tests: 响应始终使用规定优先级，未序列化 cause。
Docs Sync: 复核本卡涉及的已公开约定。
Done Definition: 完成下列验收，记录验证命令、状态及剩余限制。
Outcome: 显式合法错误 ID 优先，缺失或无效时使用请求 ID。错误原因只保留在错误链。

## 证据与影响

第 9 节检查号为 6,21,27,38。证据状态为补证。

WriteError 可能同时收到错误构造器与请求上下文中的关联 ID。前轮重点为普通响应。 [contract/response.go:114](../../../contract/response.go#L114)

[复审报告](../../../docs/evidence/stable-quality-review-d224acbc.md)

## 验收与验证

- [x] 响应始终使用规定优先级，未序列化 cause。
- [x] 记录运行结果。若已有用例覆盖目标，记录用例及相关断言。

```sh
go test -race -timeout 60s ./contract
```

## 实施证据

- [TestErrorRequestIDFallbackAndCauseIsolation](../../../contract/response_test.go#L804)

[批次验证与基准](../review-d224acbc/README.md)
