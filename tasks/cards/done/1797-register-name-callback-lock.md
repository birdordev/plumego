# Card 1797 — 健康组件命名回调重入时注册互等

Milestone: 稳定模块质量复审
Recipe: specs/change-recipes/fix-bug.yaml
Context Package: implementation
Priority: P1
State: done
Primary Module: x/observability
Owned Files: `x/observability/ops/healthhttp/manager.go`, `x/observability/ops/healthhttp/manager_test.go`, `docs/modules/x/observability/ops/README.md`
Depends On: 无

Goal: 回调可读现有配置且注册完成，关闭期间不发布组件，空名字仍拒绝。
Scope: 在锁外取得并校验名字，获取锁后重新核对关闭与注册状态再发布。
Non-goals: 维持稳定公开签名与标准库依赖，将验证限于本卡范围。
Files: `x/observability/ops/healthhttp/manager.go`, `x/observability/ops/healthhttp/manager_test.go`, `docs/modules/x/observability/ops/README.md`。
Tests: 回调可读现有配置且注册完成，关闭期间不发布组件，空名字仍拒绝。
Docs Sync: 复核模块说明并验证生成数据。
Done Definition: 完成下列验收，记录验证命令、状态及剩余限制。
Outcome: Name 回调移到 manager 锁外，发布前再检查关闭、空名和重复状态。

## 证据与影响

第 9 节检查号为 5,29,33,42,47。证据状态为已复现。

注册持 manager 写锁调用注入 checker.Name。合成 Name 同步读取 GetConfig 时注册不能完成，其他需要该锁的操作也会受阻。 [x/observability/ops/healthhttp/manager.go:158](../../../x/observability/ops/healthhttp/manager.go#L158)

[复审报告](../../../docs/evidence/stable-quality-review-a074eadb.md)

## 验收与验证

- [x] 回调可读现有配置且注册完成，关闭期间不发布组件，空名字仍拒绝。
- [x] 记录运行结果。若已有用例覆盖目标，记录用例及相关断言。

```sh
go test -race -timeout 60s ./x/observability/ops/healthhttp
make website-sync
make website-check-generated
```

## 实施证据

- [TestAcceptanceRegisterNameRunsOutsideManagerLock](../../../x/observability/ops/healthhttp/manager_test.go#L174)

[批次验证与基准](../review-a074eadb/README.md)
