# Card 1884 — 联测部分数据库句柄的清理与安全诊断

Milestone: 稳定模块质量复审
Recipe: specs/change-recipes/add-acceptance-tests.yaml
Context Package: implementation
Priority: P2
State: done
Primary Module: store
Owned Files: `store/db/sql_test.go`
Depends On: 无

Goal: 两种原因可用 errors.Is/As，默认诊断固定，已关闭池不再复用。
Scope: 通过驱动桩先建立握手连接，返回部分打开错误与独立清理错误，另以可识别的合成类型确认 errors.As 仍能取得独立清理原因。
Non-goals: 维持稳定公开签名与标准库依赖，将验证限于本卡范围。
Files: `store/db/sql_test.go`。
Tests: 两种原因可用 errors.Is/As，默认诊断固定，已关闭池不再复用。
Docs Sync: 记录核验结论；只在现有契约文字需要澄清时另提文档变更。
Done Definition: 完成下列验收，记录验证命令、状态及剩余限制。
Outcome: 部分数据库池关闭失败时依然保留 typed cleanup cause 与 opener 原因，默认错误文本固定。

## 证据与影响

第 9 节检查号为 22,25,29,30,41。证据状态为补证。

注入 opener 同时返回句柄和错误时由 OpenWith 关闭句柄并保留原因。现有用例分别覆盖部分资源关闭和安全默认诊断。需在主因和清理原因都失败时确认默认文本。 [store/db/sql.go:164](../../../store/db/sql.go#L164)

[复审报告](../../../docs/evidence/stable-quality-review-a8c2f28a.md)

## 验收与验证

- [x] 两种原因可用 errors.Is/As，默认诊断固定，已关闭池不再复用。
- [x] 记录挂载入口、具体断言和结果。已有测试能覆盖时复用并注明。

```sh
go test -race -timeout 60s ./store/db
```

## 实施证据

- [TestPartialPoolFailurePreservesTypedCloseCauseAndFixedMessage](../../../store/db/sql_test.go#L1495)

[批次验证与基准](../review-a8c2f28a/README.md)
