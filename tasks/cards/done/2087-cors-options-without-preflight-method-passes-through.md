# Card 2087 — 缺预检方法的 OPTIONS 保留应用处理

Milestone: 稳定模块第 9 节复审
Recipe: specs/change-recipes/add-acceptance-tests.yaml
Context Package: implementation
Priority: P2
State: done
Primary Module: middleware/cors
Owned Files: `middleware/cors/cors_test.go`
Depends On: 无

Goal: 缺预检方法的 OPTIONS 保留应用处理
Scope: 对照同一合法 Origin 的普通 OPTIONS 和标准预检，使用计数 handler。
Non-goals: 范围限于本卡涉及的契约，保持公开签名和依赖。
Files: `middleware/cors/cors_test.go`。
Tests: 仅普通 OPTIONS 到达 handler，已配置的协议头保留。
Docs Sync: 核对新增验收与已有模块说明一致，记录证据和限制。
Done Definition: 完成以下验收，记录命令、结果与剩余限制。
Outcome: 普通 OPTIONS 继续进入应用，标准预检由 middleware 直接完成，二者保留相应来源设置。

## 证据与影响

证据状态为补证。第 9 节检查号为 18,20,39,46。 [质量规则第 9 节](../../../docs/operations/agent-code-quality-rules.md#L267)

有 Origin 的 OPTIONS 只有带请求方法才进入预检。 [middleware/cors/cors.go:151](../../../middleware/cors/cors.go#L151)

[复审报告](../../../docs/evidence/stable-quality-review-2f962d21.md)

## 验收与验证

- [x] 仅普通 OPTIONS 到达 handler，已配置的协议头保留。
- [x] 已有测试覆盖相同触发和断言时可直接复用，记录具体用例。

```sh
go test -race -timeout 60s ./middleware/cors
```

## 实施证据

- [TestOptionsNeedsRequestedMethodToHandlePreflight](../../../middleware/cors/cors_test.go#L767)
