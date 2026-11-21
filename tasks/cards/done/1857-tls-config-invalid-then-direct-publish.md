# Card 1857 — 核对无效 TLS 配置时直接 HTTP 与显式准备的状态隔离

Milestone: 稳定模块质量复审
Recipe: specs/change-recipes/add-acceptance-tests.yaml
Context Package: implementation
Priority: P2
State: done
Primary Module: core
Owned Files: `core/core_public_test.go`
Depends On: 无

Goal: 普通 HTTP 两次分派一致，Prepare 两次均报配置问题，Server 无已发布实例，无后台诊断任务。
Scope: 调用公开 Prepare、ServeHTTP、Server 和 Shutdown，观测同一 App 在配置缺失时的状态与路由冻结点。
Non-goals: 维持稳定公开签名与标准库依赖，将验证限于本卡范围。
Files: `core/core_public_test.go`。
Tests: 普通 HTTP 两次分派一致，Prepare 两次均报配置问题，Server 无已发布实例，无后台诊断任务。
Docs Sync: 记录核验结论；只在现有契约文字需要澄清时另提文档变更。
Done Definition: 完成下列验收，记录验证命令、状态及剩余限制。
Outcome: 无效 TLS 配置时 Prepare 保持失败，直接 HTTP 发布 handler 并可重复分派。Server 与 Shutdown 不把 handler 视为已准备服务器。

## 证据与影响

第 9 节检查号为 6,15,28,31,36。证据状态为补证。

现有测试覆盖证书文件恢复及先发布 handler 后准备。补证永久缺失配置时直接 ServeHTTP 后，多次 Prepare 与 Shutdown 各自的状态。 [core/http_handler.go:73](../../../core/http_handler.go#L73)

[复审报告](../../../docs/evidence/stable-quality-review-a8c2f28a.md)

## 验收与验证

- [x] 普通 HTTP 两次分派一致，Prepare 两次均报配置问题，Server 无已发布实例，无后台诊断任务。
- [x] 记录挂载入口、具体断言和结果。已有测试能覆盖时复用并注明。

```sh
go test -race -timeout 60s ./core
```

## 实施证据

- [TestInvalidTLSConfigKeepsDirectHandlerSeparateFromServer](../../../core/core_public_test.go#L706)

[批次验证与基准](../review-a8c2f28a/README.md)
