# Card 1937 — 核对 data dir 使用普通符号链接别名时的锁归属

Milestone: 稳定模块质量复审
Recipe: specs/change-recipes/add-acceptance-tests.yaml
Context Package: implementation
Priority: P2
State: done
Primary Module: store
Owned Files: `store/kv/state_regression_test.go`
Depends On: 无

Goal: 锁竞争分类或文档化的平台限制明确，不丢数据；实际平台不支持时记录。
Scope: 仅在临时目录建普通目录别名，打开一次后尝试从另一路径打开，关闭后用别名重新读。
Non-goals: 维持稳定公开签名与标准库依赖，将验证限于本卡范围。
Files: `store/kv/state_regression_test.go`。
Tests: 锁竞争分类或文档化的平台限制明确，不丢数据；实际平台不支持时记录。
Docs Sync: 记录核验结论；只在现有契约文字需要澄清时另提文档变更。
Done Definition: 完成下列验收，记录验证命令、状态及剩余限制。
Outcome: 本机普通目录别名与原路径竞争同一 KV 锁；释放后用别名可以读取原值。

## 证据与影响

第 9 节检查号为 22,28,29,41,47。证据状态为补证。

构造前归一化真实路径，两个别名应汇入同一目录锁。需明确文件未被第二次初始化及关闭后可重开。 [store/kv/kv.go:84](../../../store/kv/kv.go#L84)

[复审报告](../../../docs/evidence/stable-quality-review-2b94cf86.md)

## 验收与验证

- [x] 锁竞争分类或文档化的平台限制明确，不丢数据；实际平台不支持时记录。
- [x] 记录挂载入口、具体断言和结果。已有测试能覆盖时复用并注明。

```sh
go test -race -timeout 60s ./store/kv
```

## 实施证据

- [TestKVDirectoryAliasSharesSingleLockAndReopens](../../../store/kv/state_regression_test.go#L833)

[批次验证与基准](../review-2b94cf86/README.md)
