# Card 2005 — 测量小页历史 CSV 的过滤与编码成本

Milestone: 稳定模块质量复审
Recipe: specs/change-recipes/add-acceptance-tests.yaml
Context Package: implementation
Priority: P3
State: done
Primary Module: x/observability/ops
Owned Files: `x/observability/ops/healthhttp/history_test.go`
Depends On: 无

Goal: 记录 ns/op、B/op 与 allocs/op，并核对 CSV 的页内行数和同查询 JSON 的完整匹配数。
Scope: 增加固定总样本与两档匹配密度的本机基准，通过普通历史入口读取一页 CSV。
Non-goals: 维持稳定公开签名与标准库依赖，将验证限于本卡范围。
Files: `x/observability/ops/healthhttp/history_test.go`。
Tests: 记录 ns/op、B/op 与 allocs/op，并核对 CSV 的页内行数和同查询 JSON 的完整匹配数。
Docs Sync: 记录核验结论；只在现有契约文字需要澄清时另提文档变更。
Done Definition: 完成下列验收，记录验证命令、状态及剩余限制。
Outcome: 两个筛选密度都返回正确 CSV 页与 JSON 全部命中数。固定 10000 条历史，本机三次中位数为密集 58738、稀疏 41347 ns/op，均为 6378 B/op 与 29 allocs/op。

## 证据与影响

第 9 节检查号为 29,35,42,48，当前证据状态为补证。 [质量规则第 9 节](../../../docs/operations/agent-code-quality-rules.md#L267)

分页已限制结果复制，CSV 还会序列化小页。稀疏匹配的扫描时间与页大小仍需分开量化。 [x/observability/ops/healthhttp/history.go:159](../../../x/observability/ops/healthhttp/history.go#L159)

[复审报告](../../../docs/evidence/stable-quality-review-999d5e4d.md)

## 验收与验证

- [x] 记录 ns/op、B/op 与 allocs/op，并核对 CSV 的页内行数和同查询 JSON 的完整匹配数。
- [x] 记录挂载入口、具体断言和结果。已有测试能覆盖时复用并注明。

```sh
go test -race -timeout 60s ./x/observability/ops/healthhttp
go test ./x/observability/ops/healthhttp -run '^$' -bench '^BenchmarkHistoryCSVFilterDensity$' -benchmem -benchtime=100ms -count=3
```

## 实施证据

- [TestHistoryCSVFilterProducesOnePageAndJSONKeepsTotal](../../../x/observability/ops/healthhttp/history_test.go#L489)
- [BenchmarkHistoryCSVFilterDensity](../../../x/observability/ops/healthhttp/history_test.go#L509)
