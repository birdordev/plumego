# Card 1762 — 补证错误详情中同源切片视图的所有权

Milestone: 稳定模块质量复审
Recipe: specs/change-recipes/add-acceptance-tests.yaml
Context Package: implementation
Priority: P2
State: done
Primary Module: contract
Owned Files: `contract/errors_test.go`
Depends On: 无

Goal: 不同长度不合并，修改能否互见符合快照约定。
Scope: 用普通 map/slice 值核对源和快照，不扩充可复制类型。
Non-goals: 维持稳定公开签名与标准库依赖，将验证限于本卡范围。
Files: `contract/errors_test.go`。
Tests: 不同长度不合并，修改能否互见符合快照约定。
Docs Sync: 复核本卡涉及的已公开约定。
Done Definition: 完成下列验收，记录验证命令、状态及剩余限制。
Outcome: 同一切片起点且长度不同的视图单独复制，修改源、一个视图及一个快照均不污染其余对象。

## 证据与影响

第 9 节检查号为 34,38,45。证据状态为补证。

引用去重使用切片类型、指针与长度。需明确不同长度、相同起点视图与容器写后读的边界。 [contract/errors.go:522](../../../contract/errors.go#L522)

[复审报告](../../../docs/evidence/stable-quality-review-d224acbc.md)

## 验收与验证

- [x] 不同长度不合并，修改能否互见符合快照约定。
- [x] 记录运行结果。若已有用例覆盖目标，记录用例及相关断言。

```sh
go test -race -timeout 60s ./contract
```

## 实施证据

- [TestErrorDetailOverlappingSliceViewsRemainIndependent](../../../contract/errors_test.go#L1222)

[批次验证与基准](../review-d224acbc/README.md)
