# Card 1772 — 核对合并方法配置切片的复制边界

Milestone: 稳定模块质量复审
Recipe: specs/change-recipes/add-acceptance-tests.yaml
Context Package: implementation
Priority: P2
State: done
Primary Module: middleware
Owned Files: `middleware/singleflight/response_regression_test.go`
Depends On: 无

Goal: 已建实例方法语义稳定，默认集合保持。
Scope: 构造后改变源切片，对照两个允许方法的 leader 计数。
Non-goals: 维持稳定公开签名与标准库依赖，将验证限于本卡范围。
Files: `middleware/singleflight/response_regression_test.go`。
Tests: 已建实例方法语义稳定，默认集合保持。
Docs Sync: 复核本卡涉及的已公开约定。
Done Definition: 完成下列验收，记录验证命令、状态及剩余限制。
Outcome: 构造后修改方法配置切片不会改变已装配实例的 leader 方法集合。

## 证据与影响

第 9 节检查号为 14,20,34,39。证据状态为补证。

构造规范化允许方法并将配置保留到并发请求，需外部修改样例核对是否复制。 [middleware/singleflight/singleflight.go:167](../../../middleware/singleflight/singleflight.go#L167)

[复审报告](../../../docs/evidence/stable-quality-review-d224acbc.md)

## 验收与验证

- [x] 已建实例方法语义稳定，默认集合保持。
- [x] 记录运行结果。若已有用例覆盖目标，记录用例及相关断言。

```sh
go test -race -timeout 60s ./middleware/singleflight
```

## 实施证据

- [TestCoalescerMethodsDetachFromCallerConfiguration](../../../middleware/singleflight/response_regression_test.go#L778)

[批次验证与基准](../review-d224acbc/README.md)
