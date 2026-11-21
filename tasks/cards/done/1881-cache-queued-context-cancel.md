# Card 1881 — 核对缓存写锁等待期间取消后的副作用

Milestone: 稳定模块质量复审
Recipe: specs/change-recipes/add-acceptance-tests.yaml
Context Package: implementation
Priority: P2
State: done
Primary Module: store
Owned Files: `store/cache/state_regression_test.go`
Depends On: 无

Goal: 未提交的写入不会改变值或使用量，记录 mutex 可取消性限制及下一次正常写入。
Scope: 以持锁屏障让合法 Set 等待，取消后释放，观察返回、已有值和统计。
Non-goals: 维持稳定公开签名与标准库依赖，将验证限于本卡范围。
Files: `store/cache/state_regression_test.go`。
Tests: 未提交的写入不会改变值或使用量，记录 mutex 可取消性限制及下一次正常写入。
Docs Sync: 记录核验结论；只在现有契约文字需要澄清时另提文档变更。
Done Definition: 完成下列验收，记录验证命令、状态及剩余限制。
Outcome: 持有写锁时取消未提交 Set。原值和统计保留，下一写入成功。Context 本身不打断 mutex。

## 证据与影响

第 9 节检查号为 26,28,31,32,41。证据状态为补证。

写入在入锁前和入锁后检查 Context。需要明确时间到达后阻塞边界及解锁后的状态。 [store/cache/memory.go:471](../../../store/cache/memory.go#L471)

[复审报告](../../../docs/evidence/stable-quality-review-a8c2f28a.md)

## 验收与验证

- [x] 未提交的写入不会改变值或使用量，记录 mutex 可取消性限制及下一次正常写入。
- [x] 记录挂载入口、具体断言和结果。已有测试能覆盖时复用并注明。

```sh
go test -race -timeout 60s ./store/cache
```

## 实施证据

- [TestCanceledSetWaitingForWriteLockPreservesCache](../../../store/cache/state_regression_test.go#L468)

[批次验证与基准](../review-a8c2f28a/README.md)
