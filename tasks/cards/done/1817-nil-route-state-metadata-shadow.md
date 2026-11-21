# Card 1817 — 核对 nil RouteState 对父请求元数据的影响

Milestone: 稳定模块质量复审
Recipe: specs/change-recipes/add-acceptance-tests.yaml
Context Package: implementation
Priority: P2
State: done
Primary Module: contract
Owned Files: `contract/context_test.go`
Depends On: 无

Goal: 路由元数据和参数按可执行约定取值，两类载体归属清晰。
Scope: 对照 nil、空状态、单参数和父请求 ID。
Non-goals: 维持稳定公开签名与标准库依赖，将验证限于本卡范围。
Files: `contract/context_test.go`。
Tests: 路由元数据和参数按可执行约定取值，两类载体归属清晰。
Docs Sync: 复核本卡涉及的已公开约定。
Done Definition: 完成下列验收，记录验证命令、状态及剩余限制。
Outcome: 空路由状态按已有上下文元数据路径读取。

## 证据与影响

第 9 节检查号为 6,27,34,38。证据状态为补证。

RouteState 与传统元数据走不同 context key。nil 载体是否遮蔽已安装父状态需要独立验证。 [contract/context_core.go:105](../../../contract/context_core.go#L105)

[复审报告](../../../docs/evidence/stable-quality-review-a074eadb.md)

## 验收与验证

- [x] 路由元数据和参数按可执行约定取值，两类载体归属清晰。
- [x] 记录运行结果。若已有用例覆盖目标，记录用例及相关断言。

```sh
go test -race -timeout 60s ./contract
```

## 实施证据

- [TestNilRouteStateFallsBackToLegacyMetadata](../../../contract/context_test.go#L206)

[批次验证与基准](../review-a074eadb/README.md)
