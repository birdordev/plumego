# Card 1657 — 补证清理无正文帧信息时保留业务头

Milestone: 稳定模块质量复审
Recipe: specs/change-recipes/add-acceptance-tests.yaml
Context Package: implementation
Priority: P2
State: done
Primary Module: contract
Owned Files: `contract/response_test.go`
Depends On: 无

Goal: 补证清理无正文帧信息时保留业务头。
Scope: 扩展现有零正文用例，将 header 编写放在响应入口前。
Non-goals: 维持公开签名和依赖；涉及现有兼容契约的调整须先核对调用边界及迁移影响。
Files: `contract/response_test.go`。
Tests: 仅清除不适用的传输字段，预设应用头、状态及一次提交保持。
Docs Sync: 对照原契约核验，若证据要求修订公开行为，先更新实施范围。
Done Definition: 完成测试与验收记录，并标明剩余取证范围。
Outcome: 204/205 清理帧字段时保留追踪、应用和缓存头，且不写正文。

## 证据

检查号为 6,23,24,38，状态为补证。

上一轮新增 204/205 帧清理，有独立真实 HTTP 和 recorder 测试。未组合应用头、请求 ID 和缓存头的保留。 [contract/response.go:31](../../../contract/response.go#L31)

[复审报告](../../../docs/evidence/stable-quality-review-09a7f5d3.md)

## 验收与验证

- [x] 仅清除不适用的传输字段，预设应用头、状态及一次提交保持。
- [x] 记录验证命令、状态和剩余范围。若使用既有用例满足验收，记录对应证据。

```sh
go test -race -timeout 60s ./contract
```

## 实施证据

[TestBodylessResponseKeepsUnrelatedHeaders](../../../contract/response_test.go#L724)
