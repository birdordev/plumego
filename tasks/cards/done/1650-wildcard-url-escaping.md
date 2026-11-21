# Card 1650 — 补证通配 URL 对百分号及 Unicode 的往返

Milestone: 稳定模块质量复审
Recipe: specs/change-recipes/add-acceptance-tests.yaml
Context Package: implementation
Priority: P2
State: done
Primary Module: router
Owned Files: `router/reverse_routing_group_test.go`
Depends On: 无

Goal: 补证通配 URL 对百分号及 Unicode 的往返。
Scope: 复用嵌套路由，生成 URL 后经公开 ServeHTTP 对照冷热结果。
Non-goals: 维持公开签名和依赖；涉及现有兼容契约的调整须先核对调用边界及迁移影响。
Files: `router/reverse_routing_group_test.go`。
Tests: 参数含空格、百分号和 Unicode 时回读原值，不改变与静态路径的优先级。
Docs Sync: 对照原契约核验，若证据要求修订公开行为，先更新实施范围。
Done Definition: 完成测试与验收记录，并标明剩余取证范围。
Outcome: 嵌套通配路由回读含百分号、空格和 Unicode 的后缀，静态匹配保持优先。

## 证据

检查号为 18,34,37,45，状态为补证。

反向通配按段转义，分派使用已解码的 URL.Path。复杂但有效的文件名要保持参数值。 [router/metadata.go:118](../../../router/metadata.go#L118)

[复审报告](../../../docs/evidence/stable-quality-review-09a7f5d3.md)

## 验收与验证

- [x] 参数含空格、百分号和 Unicode 时回读原值，不改变与静态路径的优先级。
- [x] 记录验证命令、状态和剩余范围。若使用既有用例满足验收，记录对应证据。

```sh
go test -race -timeout 60s ./router
```

## 实施证据

[TestWildcardURLRoundTripsEscapedSuffixFromNestedGroup](../../../router/reverse_routing_group_test.go#L442)
