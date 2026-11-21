# Card 1688 — 核对 ObserveHTTP 的 fan-out 取消传播

Milestone: 稳定模块质量复审
Recipe: specs/change-recipes/add-acceptance-tests.yaml
Context Package: implementation
Priority: P2
State: done
Primary Module: metrics
Owned Files: `metrics/multi_test.go`
Depends On: 无

Goal: 核对 ObserveHTTP 的 fan-out 取消传播。
Scope: 用两个子观察者记录 ctx 及全部 HTTP 参数。
Non-goals: 维持公开签名和依赖；涉及现有兼容契约的调整须先核对调用边界及迁移影响。
Files: `metrics/multi_test.go`。
Tests: 调用顺序、取消状态、原始 byte/duration 值准确，收集器回调锁边界明确。
Docs Sync: 对照原契约核验，若证据要求修订公开行为，先更新实施范围。
Done Definition: 完成测试与验收记录，并标明剩余取证范围。
Outcome: 两个子观察者按注册顺序收到同一已取消 Context 和完整 HTTP 参数。

## 证据

检查号为 26,33,44,47，状态为补证。

上一轮验证 Record 的上下文和 label 隔离。本入口按注册顺序转发 HTTP 参数，取消仍由子收集器负责。 [metrics/multi.go:63](../../../metrics/multi.go#L63)

[复审报告](../../../docs/evidence/stable-quality-review-09a7f5d3.md)

## 验收与验证

- [x] 调用顺序、取消状态、原始 byte/duration 值准确，收集器回调锁边界明确。
- [x] 记录验证命令、状态和剩余范围。若使用既有用例满足验收，记录对应证据。

```sh
go test -race -timeout 60s ./metrics
```

## 实施证据

[TestMultiObserveHTTPPropagatesContextAndObservationInOrder](../../../metrics/multi_test.go#L486)
