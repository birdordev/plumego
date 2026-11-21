# Card 1600 — 补证观测链顺序与路由上下文快照

Milestone: 本轮质量审查
Recipe: specs/change-recipes/add-acceptance-tests.yaml
Context Package: implementation
Priority: P2
State: done
Primary Module: middleware
Owned Files: `middleware/conformance/response_writer_contract_test.go`
Depends On: 无

Goal: 补证观测链顺序与路由上下文快照。
Scope: 复用 conformance 测试，把观察者放在 router 和 timeout 外侧。
Non-goals: 保留现有公开签名、依赖与模块边界。测试若证实新的缺陷，先记录证据再单独安排修复。
Files: `middleware/conformance/response_writer_contract_test.go`。
Tests: 断言每个观察者只收到一条终态记录，route、status 和 bytes 语义一致。
Docs Sync: 本卡按现有契约验证。若取证表明契约需调整，先更新后续卡范围。
Done Definition: 完成下列验收和验证，记录证据与剩余风险。
Outcome: 真实 router/timeout 装配中两个观察器各收到一次同样的路由模板、状态和字节数。

## 证据与影响

- 检查号：20,24,27,39,47。证据状态：补证。
- 观测包装可通过路由上下文感知匹配结果，需要组合验证多个观测器获得一致路由标签。 [middleware/accesslog/accesslog.go:29](../../../middleware/accesslog/accesslog.go#L29)
- [审查报告](../../../docs/evidence/stable-quality-review-dee7aa0f.md)

## 验收与验证

- [x] 断言每个观察者只收到一条终态记录，route、status 和 bytes 语义一致。
- [x] 运行以下命令并记录结果、失败路径和未验证范围。修改 Go 文件前完成仓库预检，验证前先执行仓库格式化。

```sh
go test -race -timeout 60s ./middleware/conformance
```

## 实施证据

[TestLayeredObserversSeeSameCommittedRoute](../../../middleware/conformance/response_writer_contract_test.go#L420)
