# Card 2007 — 核对已准备 Server 的请求保留调用方 context

Milestone: 稳定模块质量复审
Recipe: specs/change-recipes/add-acceptance-tests.yaml
Context Package: implementation
Priority: P3
State: done
Primary Module: core
Owned Files: `core/core_public_test.go`
Depends On: 无

Goal: 两条正常入口保持 context 值和取消状态一致。
Scope: 由公开 Server 取得 handler，传入带本地 context 值的合成请求，对比直接 ServeHTTP。
Non-goals: 维持稳定公开签名与标准库依赖，将验证限于本卡范围。
Files: `core/core_public_test.go`。
Tests: 两条正常入口保持 context 值和取消状态一致。
Docs Sync: 记录核验结论；只在现有契约文字需要澄清时另提文档变更。
Done Definition: 完成下列验收，记录验证命令、状态及剩余限制。
Outcome: 预备 Server.Handler 与 App.ServeHTTP 保留调用者 context 值和预取消状态。

## 证据与影响

第 9 节检查号为 5,22,25,36，当前证据状态为补证。 [质量规则第 9 节](../../../docs/operations/agent-code-quality-rules.md#L267)

Server 返回准备好的 HTTP handler。调用标准入口时应保留自定义 context 值和取消语义。 [core/lifecycle.go:59](../../../core/lifecycle.go#L59)

[复审报告](../../../docs/evidence/stable-quality-review-999d5e4d.md)

## 验收与验证

- [x] 两条正常入口保持 context 值和取消状态一致。
- [x] 记录挂载入口、具体断言和结果。已有测试能覆盖时复用并注明。

```sh
go test -race -timeout 60s ./core
```

## 实施证据

- [TestPreparedServerPreservesRequestContext](../../../core/core_public_test.go#L827)
