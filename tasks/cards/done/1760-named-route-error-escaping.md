# Card 1760 — 核对反向路由参数数量错误的诊断范围

Milestone: 稳定模块质量复审
Recipe: specs/change-recipes/add-acceptance-tests.yaml
Context Package: implementation
Priority: P3
State: done
Primary Module: router
Owned Files: `router/reverse_routing_group_test.go`
Depends On: 无

Goal: 错误后命名表保持，合法 URL 仍与 dispatch 一致。
Scope: 复用有名路由样例，锁定错误分类以及余下的有效反向路径。
Non-goals: 维持稳定公开签名与标准库依赖，将验证限于本卡范围。
Files: `router/reverse_routing_group_test.go`。
Tests: 错误后命名表保持，合法 URL 仍与 dispatch 一致。
Docs Sync: 复核本卡涉及的已公开约定。
Done Definition: 完成下列验收，记录验证命令、状态及剩余限制。
Outcome: 参数成对、未知及重复错误后，命名项仍能生成带转义的合法 URL 并 dispatch。

## 证据与影响

第 9 节检查号为 16,22,37。证据状态为补证。

存在未知、缺少和重复参数等多条诊断入口；数量错误与成功 URL 编码应独立。 [router/metadata.go:51](../../../router/metadata.go#L51)

[复审报告](../../../docs/evidence/stable-quality-review-d224acbc.md)

## 验收与验证

- [x] 错误后命名表保持，合法 URL 仍与 dispatch 一致。
- [x] 记录运行结果。若已有用例覆盖目标，记录用例及相关断言。

```sh
go test -race -timeout 60s ./router
```

## 实施证据

- [TestURLMalformedParamPairsReturnEmpty](../../../router/reverse_routing_group_test.go#L114)

[批次验证与基准](../review-d224acbc/README.md)
