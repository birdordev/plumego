# Card 2113 — HTTP 记录经多子分发的快照链

Milestone: 稳定模块第 9 节复审
Recipe: specs/change-recipes/add-acceptance-tests.yaml
Context Package: implementation
Priority: P3
State: done
Primary Module: metrics
Owned Files: `metrics/multi_test.go`
Depends On: 无

Goal: HTTP 记录经多子分发的快照链
Scope: 创建两个独立 HTTP record，合流的先接收者修改普通标签。
Non-goals: 范围限于本卡涉及的契约，保持公开签名和依赖。
Files: `metrics/multi_test.go`。
Tests: 后接收者、调用方和另一条 record 保留各自标签，时长仍以秒表达。
Docs Sync: 核对新增验收与已有模块说明一致，记录证据和限制。
Done Definition: 完成以下验收，记录命令、结果与剩余限制。
Outcome: 一个子项改写 HTTP 标签时，下一子项及两份原 HTTP record 保留自己的标签与秒单位。

## 证据与影响

证据状态为补证。第 9 节检查号为 5,34,44,45。 [质量规则第 9 节](../../../docs/operations/agent-code-quality-rules.md#L267)

HTTP record 元数据形状与通用 Record fanout 复制分散在两个公开入口。 [metrics/collector.go:49](../../../metrics/collector.go#L49)

[复审报告](../../../docs/evidence/stable-quality-review-2f962d21.md)

## 验收与验证

- [x] 后接收者、调用方和另一条 record 保留各自标签，时长仍以秒表达。
- [x] 已有测试覆盖相同触发和断言时可直接复用，记录具体用例。

```sh
go test -race -timeout 60s ./metrics
```

## 实施证据

- [TestHTTPRecordFanoutKeepsCallerAndSiblingLabelsIsolated](../../../metrics/multi_test.go#L655)
