# Card 1745 — 无路由告警使已经发布的 Prepare 抛出 panic

Milestone: 稳定模块质量复审
Recipe: specs/change-recipes/fix-bug.yaml
Context Package: implementation
Priority: P1
State: done
Primary Module: core
Owned Files: `core/http_handler.go`, `core/lifecycle_test.go`, `docs/modules/core/README.md`
Depends On: 无

Goal: 分别检查显式 Prepare 和直接 ServeHTTP，告警出错时正常返回且没有额外告警。
Scope: 按现有关停诊断的约定隔离该告警回调的 panic，保留 handler 与服务器发布规则。
Non-goals: 维持稳定公开签名与标准库依赖，将验证限于本卡范围。
Files: `core/http_handler.go`, `core/lifecycle_test.go`, `docs/modules/core/README.md`。
Tests: 分别检查显式 Prepare 和直接 ServeHTTP，告警出错时正常返回且没有额外告警。
Docs Sync: 复核模块说明并验证生成数据。
Done Definition: 完成下列验收，记录验证命令、状态及剩余限制。
Outcome: 空路由警告的 panic 限制在诊断路径，直接 ServeHTTP 可返回 404，后续 Prepare 与 Server 保持稳定且只告警一次。

## 证据与影响

第 9 节检查号为 22,28,31,33,36。证据状态为已复现。

应用没有路由且注入 logger 的 Warn 抛 panic 时，首次 Prepare 抛出异常，同一个 App.Server 已可取到服务器，再次 Prepare 成功。 [core/http_handler.go:110](../../../core/http_handler.go#L110)

[复审报告](../../../docs/evidence/stable-quality-review-d224acbc.md)

## 验收与验证

- [x] 分别检查显式 Prepare 和直接 ServeHTTP，告警出错时正常返回且没有额外告警。
- [x] 记录运行结果。若已有用例覆盖目标，记录用例及相关断言。

```sh
go test -race -timeout 60s ./core
make website-sync
make website-check-generated
```

## 实施证据

- [TestAcceptanceMissingRouteWarningDoesNotInterruptPublication](../../../core/lifecycle_test.go#L1217)

[批次验证与基准](../review-d224acbc/README.md)
