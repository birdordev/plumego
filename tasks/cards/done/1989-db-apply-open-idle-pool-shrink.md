# Card 1989 — 核对 SQL 连接池上限收缩后的配置

Milestone: 稳定模块质量复审
Recipe: specs/change-recipes/add-acceptance-tests.yaml
Context Package: implementation
Priority: P3
State: done
Primary Module: store/db
Owned Files: `store/db/sql_test.go`
Depends On: 无

Goal: 稳定后的 OpenConnections 与 MaxOpenConnections 满足配置，连接可继续使用。
Scope: 用本机 driver 创建多连接，应用更小的合法配置后观察 DBStats。
Non-goals: 维持稳定公开签名与标准库依赖，将验证限于本卡范围。
Files: `store/db/sql_test.go`。
Tests: 稳定后的 OpenConnections 与 MaxOpenConnections 满足配置，连接可继续使用。
Docs Sync: 记录核验结论；只在现有契约文字需要澄清时另提文档变更。
Done Definition: 完成下列验收，记录验证命令、状态及剩余限制。
Outcome: 已建立三条空闲连接的 SQL 池可缩至一个打开和空闲连接，后续 Ping 正常。

## 证据与影响

第 9 节检查号为 14,28,34,41，当前证据状态为补证。 [质量规则第 9 节](../../../docs/operations/agent-code-quality-rules.md#L267)

ApplyConfig 分开设置 open 与 idle 参数。收缩上限时，调用者能观察到的池统计应与当前契约一致。 [store/db/sql.go:189](../../../store/db/sql.go#L189)

[复审报告](../../../docs/evidence/stable-quality-review-999d5e4d.md)

## 验收与验证

- [x] 稳定后的 OpenConnections 与 MaxOpenConnections 满足配置，连接可继续使用。
- [x] 记录挂载入口、具体断言和结果。已有测试能覆盖时复用并注明。

```sh
go test -race -timeout 60s ./store/db
```

## 实施证据

- [TestApplyConfigShrinksOpenAndIdlePool](../../../store/db/sql_test.go#L1603)
