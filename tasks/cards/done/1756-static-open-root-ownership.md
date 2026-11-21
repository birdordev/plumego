# Card 1756 — 验证本地静态根的生命周期与边界约定

Milestone: 稳定模块质量复审
Recipe: specs/change-recipes/analysis-only.yaml
Context Package: review
Priority: P2
State: done
Primary Module: router
Owned Files: `router/static_test.go`
Depends On: 无

Goal: 失效路径只返回固定 404，不暴露路径，正常引用仍可读取。
Scope: 用受控目录失效和正常挂载验证错误响应；评估独立根句柄的资源归属。
Non-goals: 维持稳定公开签名与标准库依赖，将验证限于本卡范围。
Files: `router/static_test.go`。
Tests: 失效路径只返回固定 404，不暴露路径，正常引用仍可读取。
Docs Sync: 复核本卡涉及的已公开约定。
Done Definition: 完成下列验收，记录验证命令、状态及剩余限制。
Outcome: 可信静态根能读取普通资产。根消失后返回固定 404，响应没有包含本机路径，维持现有根所有权。

## 证据与影响

第 9 节检查号为 16,29,37,46。证据状态为补证。

源码明确限定只读或可信根。需要确认根不可用时的关闭行为，以及静态根部署说明与当前边界一致。 [router/static.go:234](../../../router/static.go#L234)

[复审报告](../../../docs/evidence/stable-quality-review-d224acbc.md)

## 验收与验证

- [x] 失效路径只返回固定 404，不暴露路径，正常引用仍可读取。
- [x] 记录运行结果。若已有用例覆盖目标，记录用例及相关断言。

```sh
go test -race -timeout 60s ./router
```

## 实施证据

- [TestStaticRootDisappearingReturnsFixedNotFound](../../../router/static_test.go#L648)
- [TestStaticServesSymlinkInsideRoot](../../../router/static_test.go#L214)

[批次验证与基准](../review-d224acbc/README.md)
