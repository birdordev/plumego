# Card 1831 — 核对失效读取与关闭后的统计生命周期

Milestone: 稳定模块质量复审
Recipe: specs/change-recipes/add-acceptance-tests.yaml
Context Package: implementation
Priority: P2
State: done
Primary Module: store
Owned Files: `store/cache/state_regression_test.go`
Depends On: 无

Goal: 无重新写入；明确读在闭合前或后的可分类结果及最终计数。
Scope: 用受控写屏障、合成过期样本和闭合后的 Stats 核对。
Non-goals: 维持稳定公开签名与标准库依赖，将验证限于本卡范围。
Files: `store/cache/state_regression_test.go`。
Tests: 无重新写入；明确读在闭合前或后的可分类结果及最终计数。
Docs Sync: 复核本卡涉及的已公开约定。
Done Definition: 完成下列验收，记录验证命令、状态及剩余限制。
Outcome: 过期读取跳过忙碌的写路径，关闭后读取返回已关闭错误，最终 Stats 保留计数但释放载荷。

## 证据与影响

第 9 节检查号为 28,32,34,41,47。证据状态为补证。

最新变更在 miss 时重查 closed。过期项已读出时可尝试 best-effort 删除，需明确与关闭屏障交叠。 [store/cache/memory.go:178](../../../store/cache/memory.go#L178)

[复审报告](../../../docs/evidence/stable-quality-review-a074eadb.md)

## 验收与验证

- [x] 无重新写入；明确读在闭合前或后的可分类结果及最终计数。
- [x] 记录运行结果。若已有用例覆盖目标，记录用例及相关断言。

```sh
go test -race -timeout 60s ./store/cache
```

## 实施证据

- [TestExpiredReadNearClosePreservesFinalAccounting](../../../store/cache/state_regression_test.go#L422)

[批次验证与基准](../review-a074eadb/README.md)
