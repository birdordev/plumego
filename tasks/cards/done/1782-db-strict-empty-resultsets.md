# Card 1782 — 补证严格查询跳过空结果集的行数契约

Milestone: 稳定模块质量复审
Recipe: specs/change-recipes/add-acceptance-tests.yaml
Context Package: implementation
Priority: P2
State: done
Primary Module: store
Owned Files: `store/db/sql_test.go`
Depends On: 无

Goal: 一行成功，多行标准错误，迭代失败保留主因。
Scope: 用数据库桩组合空集、单行和跨集第二行，检查关闭。
Non-goals: 维持稳定公开签名与标准库依赖，将验证限于本卡范围。
Files: `store/db/sql_test.go`。
Tests: 一行成功，多行标准错误，迭代失败保留主因。
Docs Sync: 复核本卡涉及的已公开约定。
Done Definition: 完成下列验收，记录验证命令、状态及剩余限制。
Outcome: 复用已有空结果集、跨集第二行、迭代错误与关闭检查矩阵，覆盖严格查询的可观察分类。

## 证据与影响

第 9 节检查号为 22,29,41。证据状态为补证。

严格查询跨结果集寻找第二行，空集和迭代失败改变分类。 [store/db/sql.go:357](../../../store/db/sql.go#L357)

[复审报告](../../../docs/evidence/stable-quality-review-d224acbc.md)

## 验收与验证

- [x] 一行成功，多行标准错误，迭代失败保留主因。
- [x] 记录运行结果。若已有用例覆盖目标，记录用例及相关断言。

```sh
go test -race -timeout 60s ./store/db
```

## 实施证据

- [TestQueryRowStrictAcrossResultSets](../../../store/db/sql_test.go#L917)

[批次验证与基准](../review-d224acbc/README.md)
