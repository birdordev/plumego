# Card 1656 — 核对请求 ID 的多字节长度与响应回显

Milestone: 稳定模块质量复审
Recipe: specs/change-recipes/add-acceptance-tests.yaml
Context Package: implementation
Priority: P2
State: done
Primary Module: contract
Owned Files: `contract/request_id_test.go`
Depends On: 无

Goal: 核对请求 ID 的多字节长度与响应回显。
Scope: 构造字符数相近但字节数分别低于、等于和超过限制的样本，覆盖公开响应。
Non-goals: 维持公开签名和依赖；涉及现有兼容契约的调整须先核对调用边界及迁移影响。
Files: `contract/request_id_test.go`。
Tests: 超过限制时保持已有合法 ID，其他输入与 JSON request_id 一致。
Docs Sync: 对照原契约核验，若证据要求修订公开行为，先更新实施范围。
Done Definition: 完成测试与验收记录，并标明剩余取证范围。
Outcome: 用 126/128/130 字节输入验证公开响应 request_id；超过限制保留原合法值。

## 证据

检查号为 18,25,27,38，状态为补证。

请求 ID 的上限按字节，使用 UTF-8 并先 TrimSpace。 [contract/request_id.go:45](../../../contract/request_id.go#L45)

[复审报告](../../../docs/evidence/stable-quality-review-09a7f5d3.md)

## 验收与验证

- [x] 超过限制时保持已有合法 ID，其他输入与 JSON request_id 一致。
- [x] 记录验证命令、状态和剩余范围。若使用既有用例满足验收，记录对应证据。

```sh
go test -race -timeout 60s ./contract
```

## 实施证据

[TestRequestIDMultibyteBoundaryIsUsedByResponse](../../../contract/request_id_test.go#L100)
