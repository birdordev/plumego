# Card 1939 — 核对 ScanRows 扫描失败时迭代部分结果及资源保留

Milestone: 稳定模块质量复审
Recipe: specs/change-recipes/add-acceptance-tests.yaml
Context Package: implementation
Priority: P2
State: done
Primary Module: store
Owned Files: `store/db/sql_test.go`
Depends On: 无

Goal: 明确旧的部分结果返回策略，两原因可分类，连接归还并可重用。
Scope: 使用本机 Rows 桩，第一项成功、第二项失败，关闭也给出不同的固定原因。
Non-goals: 维持稳定公开签名与标准库依赖，将验证限于本卡范围。
Files: `store/db/sql_test.go`。
Tests: 明确旧的部分结果返回策略，两原因可分类，连接归还并可重用。
Docs Sync: 记录核验结论；只在现有契约文字需要澄清时另提文档变更。
Done Definition: 完成下列验收，记录验证命令、状态及剩余限制。
Outcome: 第二项扫描失败后按现有契约丢弃部分结果，保留 scan 和 Close 原因，底层连接归还。

## 证据与影响

第 9 节检查号为 22,29,30,41。证据状态为补证。

扫描回调失败时会结束迭代；关闭错误与主原因通过专用 helper 合并。需保证已积累记录和返回形状符合合同。 [store/db/sql.go:385](../../../store/db/sql.go#L385)

[复审报告](../../../docs/evidence/stable-quality-review-2b94cf86.md)

## 验收与验证

- [x] 明确旧的部分结果返回策略，两原因可分类，连接归还并可重用。
- [x] 记录挂载入口、具体断言和结果。已有测试能覆盖时复用并注明。

```sh
go test -race -timeout 60s ./store/db
```

## 实施证据

- [TestScanRowsDiscardsPartialResultsAndRetainsCloseCause](../../../store/db/sql_test.go#L1555)

[批次验证与基准](../review-2b94cf86/README.md)
