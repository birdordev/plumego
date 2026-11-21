# Card 1743 — 单组件失败探测改写 Manager 持有的快照

Milestone: 稳定模块质量复审
Recipe: specs/change-recipes/fix-bug.yaml
Context Package: implementation
Priority: P1
State: done
Primary Module: x/observability
Owned Files: `x/observability/ops/healthhttp/handlers.go`, `x/observability/ops/healthhttp/handlers_test.go`, `docs/modules/x/observability/ops/README.md`
Depends On: 无

Goal: 公开单组件入口返回原有 503 及固定错误文案，借用对象不变。
Scope: 以局部值构造失败响应，仅在局部值上覆盖状态和消息。
Non-goals: 维持稳定公开签名与标准库依赖，将验证限于本卡范围。
Files: `x/observability/ops/healthhttp/handlers.go`, `x/observability/ops/healthhttp/handlers_test.go`, `docs/modules/x/observability/ops/README.md`。
Tests: 公开单组件入口返回原有 503 及固定错误文案，借用对象不变。
Docs Sync: 复核模块说明并验证生成数据。
Done Definition: 完成下列验收，记录验证命令、状态及剩余限制。
Outcome: 失败响应从局部值派生，保留规范 503 和固定文案，注入 Manager 的健康对象及历史消息保持。

## 证据与影响

第 9 节检查号为 6,23,34,42,45。证据状态为已复现。

注入的 Manager 返回借用的健康快照时，失败处理同时修改状态和消息；503 已发送，调用方持有的数据也被写成 unhealthy。内置 Manager 返回副本，受影响面是自定义实现。 [x/observability/ops/healthhttp/handlers.go:118](../../../x/observability/ops/healthhttp/handlers.go#L118)

[复审报告](../../../docs/evidence/stable-quality-review-d224acbc.md)

## 验收与验证

- [x] 公开单组件入口返回原有 503 及固定错误文案，借用对象不变。
- [x] 记录运行结果。若已有用例覆盖目标，记录用例及相关断言。

```sh
go test -race -timeout 60s ./x/observability/ops/healthhttp
make website-sync
make website-check-generated
```

## 实施证据

- [TestAcceptanceComponentFailureLeavesBorrowedSnapshotIntact](../../../x/observability/ops/healthhttp/handlers_test.go#L618)

[批次验证与基准](../review-d224acbc/README.md)
