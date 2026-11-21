# Card 1997 — 核对 JSON 日志中合法非 ASCII 字段名的稳定保留

Milestone: 稳定模块质量复审
Recipe: specs/change-recipes/add-acceptance-tests.yaml
Context Package: implementation
Priority: P3
State: done
Primary Module: log
Owned Files: `log/json_test.go`
Depends On: 无

Goal: 保留字段名和对应值，输出可解码且观察结果稳定。
Scope: 在预算内使用中英文合成字段及普通标量，重复记录并解码。
Non-goals: 维持稳定公开签名与标准库依赖，将验证限于本卡范围。
Files: `log/json_test.go`。
Tests: 保留字段名和对应值，输出可解码且观察结果稳定。
Docs Sync: 记录核验结论；只在现有契约文字需要澄清时另提文档变更。
Done Definition: 完成下列验收，记录验证命令、状态及剩余限制。
Outcome: 预算内的中英文 JSON 字段名及对应值重复输出后保持可解码。

## 证据与影响

第 9 节检查号为 16,29,31,43，当前证据状态为补证。 [质量规则第 9 节](../../../docs/operations/agent-code-quality-rules.md#L267)

字段 map 归一化按确定顺序处理，并受工作预算。常规非 ASCII 字段名才是本卡的输入范围。 [log/json.go:366](../../../log/json.go#L366)

[复审报告](../../../docs/evidence/stable-quality-review-999d5e4d.md)

## 验收与验证

- [x] 保留字段名和对应值，输出可解码且观察结果稳定。
- [x] 记录挂载入口、具体断言和结果。已有测试能覆盖时复用并注明。

```sh
go test -race -timeout 60s ./log
```

## 实施证据

- [TestJSONLoggerPreservesUnicodeFieldNames](../../../log/json_test.go#L1023)
