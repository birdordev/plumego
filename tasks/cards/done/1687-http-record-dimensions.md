# Card 1687 — 验证 HTTP record 的 labels 可变所有权与单位

Milestone: 稳定模块质量复审
Recipe: specs/change-recipes/add-acceptance-tests.yaml
Context Package: implementation
Priority: P2
State: done
Primary Module: metrics
Owned Files: `metrics/collector_test.go`
Depends On: 无

Goal: 验证 HTTP record 的 labels 可变所有权与单位。
Scope: 构造相邻请求记录并修改第一个所返回 labels。
Non-goals: 维持公开签名和依赖；涉及现有兼容契约的调整须先核对调用边界及迁移影响。
Files: `metrics/collector_test.go`。
Tests: 第二个值保持独立，时间和单位遵循公开契约。
Docs Sync: 对照原契约核验，若证据要求修订公开行为，先更新实施范围。
Done Definition: 完成测试与验收记录，并标明剩余取证范围。
Outcome: 相邻 HTTP 记录的 labels 独立，耗时单位与时间戳通过公开入口验证。

## 证据

检查号为 16,34,44,45，状态为补证。

NewHTTPRecord 负责构建 labels、秒数与时间，调用者可能复用多个测量维度。 [metrics/collector.go:48](../../../metrics/collector.go#L48)

[复审报告](../../../docs/evidence/stable-quality-review-09a7f5d3.md)

## 验收与验证

- [x] 第二个值保持独立，时间和单位遵循公开契约。
- [x] 记录验证命令、状态和剩余范围。若使用既有用例满足验收，记录对应证据。

```sh
go test -race -timeout 60s ./metrics
```

## 实施证据

[TestNewHTTPRecordOwnsLabelsForEachObservation](../../../metrics/collector_test.go#L233) · [TestNewHTTPRecordUsesCanonicalShape](../../../metrics/collector_test.go#L77)
