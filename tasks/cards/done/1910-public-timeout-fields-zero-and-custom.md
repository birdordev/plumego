# Card 1910 — 核对准备后混合零值和显式超时的配置透传

Milestone: 稳定模块质量复审
Recipe: specs/change-recipes/add-acceptance-tests.yaml
Context Package: implementation
Priority: P3
State: done
Primary Module: core
Owned Files: `core/core_public_test.go`
Depends On: 无

Goal: 保留显式配置和零值，不人为写入另一层默认值。
Scope: 将默认配置的两种超时设为零，再设置另一项为固定正值，比较两个独立 App 的 Server 字段。
Non-goals: 维持稳定公开签名与标准库依赖，将验证限于本卡范围。
Files: `core/core_public_test.go`。
Tests: 保留显式配置和零值，不人为写入另一层默认值。
Docs Sync: 记录核验结论；只在现有契约文字需要澄清时另提文档变更。
Done Definition: 完成下列验收，记录验证命令、状态及剩余限制。
Outcome: 准备后的 Server 保留配置中的显式零值和正值，具体协议侧默认计算由 net/http 拥有。

## 证据与影响

第 9 节检查号为 6,14,16,36。证据状态为补证。

准备直接安装配置字段，HTTP 层对个别零值由 net/http 解释。当前需核对约定描述和准备产物。 [core/http_handler.go:152](../../../core/http_handler.go#L152)

[复审报告](../../../docs/evidence/stable-quality-review-2b94cf86.md)

## 验收与验证

- [x] 保留显式配置和零值，不人为写入另一层默认值。
- [x] 记录挂载入口、具体断言和结果。已有测试能覆盖时复用并注明。

```sh
go test -race -timeout 60s ./core
```

## 实施证据

- [TestPreparedServerPreservesMixedExplicitTimeoutFields](../../../core/core_public_test.go#L804)

[批次验证与基准](../review-2b94cf86/README.md)
