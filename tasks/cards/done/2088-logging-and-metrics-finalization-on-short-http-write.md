# Card 2088 — 访问日志的部分写入字节观察

Milestone: 稳定模块第 9 节复审
Recipe: specs/change-recipes/add-acceptance-tests.yaml
Context Package: implementation
Priority: P2
State: done
Primary Module: middleware/accesslog
Owned Files: `middleware/accesslog/accesslog_test.go`
Depends On: 无

Goal: 访问日志的部分写入字节观察
Scope: 注入可控短写 writer与内存日志器，记录匹配路径、状态和字节。
Non-goals: 范围限于本卡涉及的契约，保持公开签名和依赖。
Files: `middleware/accesslog/accesslog_test.go`。
Tests: 最终只有一条记录，计量为真实字节，日志不包含写入内容。
Docs Sync: 核对新增验收与已有模块说明一致，记录证据和限制。
Done Definition: 完成以下验收，记录命令、结果与剩余限制。
Outcome: 复用访问日志短写回归。底层接收两字节后返回错误，最终一次日志记录实际状态、字节和匹配路由。

## 证据与影响

证据状态为补证。第 9 节检查号为 23,24,25,39,45。 [质量规则第 9 节](../../../docs/operations/agent-code-quality-rules.md#L267)

访问日志和指标使用同一 transport 记录策略，日志侧单独核验底层接受字节。 [middleware/accesslog/accesslog.go:23](../../../middleware/accesslog/accesslog.go#L23)

[复审报告](../../../docs/evidence/stable-quality-review-2f962d21.md)

## 验收与验证

- [x] 最终只有一条记录，计量为真实字节，日志不包含写入内容。
- [x] 已有测试覆盖相同触发和断言时可直接复用，记录具体用例。

```sh
go test -race -timeout 60s ./middleware/accesslog
```

## 实施证据

- [TestAccessLogRecordsMatchedRouteAfterShortWrite](../../../middleware/accesslog/accesslog_test.go#L336)
