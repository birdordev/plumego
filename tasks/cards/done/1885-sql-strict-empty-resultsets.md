# Card 1885 — 核对 Strict 单行读取中回调取消后的清理

Milestone: 稳定模块质量复审
Recipe: specs/change-recipes/add-acceptance-tests.yaml
Context Package: implementation
Priority: P2
State: done
Primary Module: store
Owned Files: `store/db/sql_test.go`
Depends On: 无

Goal: 只有一个 scan 调用，关闭只发生一次，明确取消原因和驱动 I/O 归属。
Scope: 在持有可取消 Context 的 scan 回调内完成读取并取消，通过本机 driver 桩观察分类、关闭及下一次池使用。
Non-goals: 维持稳定公开签名与标准库依赖，将验证限于本卡范围。
Files: `store/db/sql_test.go`。
Tests: 只有一个 scan 调用，关闭只发生一次，明确取消原因和驱动 I/O 归属。
Docs Sync: 记录核验结论；只在现有契约文字需要澄清时另提文档变更。
Done Definition: 完成下列验收，记录验证命令、状态及剩余限制。
Outcome: Strict 扫描回调取消后只关闭一次，余下结果集未读取，池归还且仍可 Ping。

## 证据与影响

第 9 节检查号为 22,26,29,30,41。证据状态为补证。

现有结果集矩阵已覆盖空集及驱动 advance 错误。补证 scan 完成后取消 Context 时其余结果集的归属。 [store/db/sql.go:357](../../../store/db/sql.go#L357)

[复审报告](../../../docs/evidence/stable-quality-review-a8c2f28a.md)

## 验收与验证

- [x] 只有一个 scan 调用，关闭只发生一次，明确取消原因和驱动 I/O 归属。
- [x] 记录挂载入口、具体断言和结果。已有测试能覆盖时复用并注明。

```sh
go test -race -timeout 60s ./store/db
```

## 实施证据

- [TestStrictRowCancellationClosesRowsAndKeepsPoolUsable](../../../store/db/sql_test.go#L1523)

[批次验证与基准](../review-a8c2f28a/README.md)
