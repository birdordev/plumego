# Card 1750 — 组件列表顺序随 map 遍历变化

Milestone: 稳定模块质量复审
Recipe: specs/change-recipes/fix-bug.yaml
Context Package: implementation
Priority: P3
State: done
Primary Module: x/observability
Owned Files: `x/observability/ops/healthhttp/handlers.go`, `x/observability/ops/healthhttp/handlers_test.go`, `docs/modules/x/observability/ops/README.md`
Depends On: 无

Goal: 不同注册顺序与同一快照多次调用给出相同名字顺序，空列表保持数组。
Scope: 在现有名字切片上排序，复核当前 API 对顺序没有相反要求。
Non-goals: 维持稳定公开签名与标准库依赖，将验证限于本卡范围。
Files: `x/observability/ops/healthhttp/handlers.go`, `x/observability/ops/healthhttp/handlers_test.go`, `docs/modules/x/observability/ops/README.md`。
Tests: 不同注册顺序与同一快照多次调用给出相同名字顺序，空列表保持数组。
Docs Sync: 复核模块说明并验证生成数据。
Done Definition: 完成下列验收，记录验证命令、状态及剩余限制。
Outcome: 组件名字按字典序稳定输出，计数和空数组保持原线格式。

## 证据与影响

第 9 节检查号为 6,16,34,42。证据状态为源码确认。

列表把 map 迭代顺序写进数组，消费者无法从相同快照得到稳定展示；组件集合和 count 本身正确。 [x/observability/ops/healthhttp/handlers.go:160](../../../x/observability/ops/healthhttp/handlers.go#L160)

[复审报告](../../../docs/evidence/stable-quality-review-d224acbc.md)

## 验收与验证

- [x] 不同注册顺序与同一快照多次调用给出相同名字顺序，空列表保持数组。
- [x] 记录运行结果。若已有用例覆盖目标，记录用例及相关断言。

```sh
go test -race -timeout 60s ./x/observability/ops/healthhttp
make website-sync
make website-check-generated
```

## 实施证据

- [TestAcceptanceComponentNamesSortedAndEmptyArrayPreserved](../../../x/observability/ops/healthhttp/handlers_test.go#L659)

[批次验证与基准](../review-d224acbc/README.md)
