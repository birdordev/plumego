# Card 1787 — 核对健康模型 details 空值及普通值的线格式

Milestone: 稳定模块质量复审
Recipe: specs/change-recipes/add-acceptance-tests.yaml
Context Package: implementation
Priority: P2
State: done
Primary Module: health
Owned Files: `health/json_contract_test.go`
Depends On: 无

Goal: 编码形状和稳定字段符合约定，宿主职责不变。
Scope: 用公共 JSON 编码与 Clone 对照三种 details。
Non-goals: 维持稳定公开签名与标准库依赖，将验证限于本卡范围。
Files: `health/json_contract_test.go`。
Tests: 编码形状和稳定字段符合约定，宿主职责不变。
Docs Sync: 复核本卡涉及的已公开约定。
Done Definition: 完成下列验收，记录验证命令、状态及剩余限制。
Outcome: 详情空容器省略，非空 map 的 null、0 值分别保留，Clone 后线格式相同。

## 证据与影响

第 9 节检查号为 6,21,38,42。证据状态为补证。

模型有 omitempty。空容器、非空且值为空和零数值是不同表示。 [health/core.go:28](../../../health/core.go#L28)

[复审报告](../../../docs/evidence/stable-quality-review-d224acbc.md)

## 验收与验证

- [x] 编码形状和稳定字段符合约定，宿主职责不变。
- [x] 记录运行结果。若已有用例覆盖目标，记录用例及相关断言。

```sh
go test -race -timeout 60s ./health
```

## 实施证据

- [TestDetailsWireShapeDistinguishesMissingMapAndNullMembers](../../../health/json_contract_test.go#L242)

[批次验证与基准](../review-d224acbc/README.md)
