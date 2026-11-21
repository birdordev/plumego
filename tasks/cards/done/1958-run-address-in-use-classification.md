# Card 1958 — 锁定 Run 已预备时的监听失败分类

Milestone: 稳定模块质量复审
Recipe: specs/change-recipes/add-acceptance-tests.yaml
Context Package: implementation
Priority: P2
State: done
Primary Module: core
Owned Files: `core/lifecycle_test.go`
Depends On: 无

Goal: 监听失败保留底层错误类别，关闭状态不会把失败覆写为成功。
Scope: 用本机占用监听地址的夹具从公开 Run 执行失败路径。
Non-goals: 维持稳定公开签名与标准库依赖，将验证限于本卡范围。
Files: `core/lifecycle_test.go`。
Tests: 监听失败保留底层错误类别，关闭状态不会把失败覆写为成功。
Docs Sync: 记录核验结论；只在现有契约文字需要澄清时另提文档变更。
Done Definition: 完成下列验收，记录验证命令、状态及剩余限制。
Outcome: 已准备的 App 遇到占用地址保留 net.OpError 的 listen 分类。Shutdown 成功且仍返回同一个已准备服务器。

## 证据与影响

第 9 节检查号为 15,28,36,46，当前证据状态为补证。 [质量规则第 9 节](../../../docs/operations/agent-code-quality-rules.md#L267)

Run 区分正常服务器关闭与其他错误。还需确认先 Prepare、绑定失败、再 Shutdown 时各自的错误和状态。 [core/lifecycle.go:39](../../../core/lifecycle.go#L39)

[复审报告](../../../docs/evidence/stable-quality-review-999d5e4d.md)

## 验收与验证

- [x] 监听失败保留底层错误类别，关闭状态不会把失败覆写为成功。
- [x] 记录挂载入口、具体断言和结果。已有测试能覆盖时复用并注明。

```sh
go test -race -timeout 60s ./core
```

## 实施证据

- [TestPreparedRunPreservesBindErrorOnShutdown](../../../core/lifecycle_test.go#L1505)
