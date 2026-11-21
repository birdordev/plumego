# Card 1843 — 核对衍生 logger 的字段合并与诊断隔离

Milestone: 稳定模块质量复审
Recipe: specs/change-recipes/add-acceptance-tests.yaml
Context Package: implementation
Priority: P2
State: done
Primary Module: log
Owned Files: `log/json_test.go`
Depends On: 无

Goal: 日志字段各属自身，诊断固定一次。
Scope: 让一个本机输出只在首次失败，隔离诊断，再写普通衍生字段。
Non-goals: 维持稳定公开签名与标准库依赖，将验证限于本卡范围。
Files: `log/json_test.go`。
Tests: 日志字段各属自身，诊断固定一次。
Docs Sync: 复核本卡涉及的已公开约定。
Done Definition: 完成下列验收，记录验证命令、状态及剩余限制。
Outcome: 两个衍生 JSON logger 写入失败只产生一次隔离诊断，后续输出各自字段和父字段。

## 证据与影响

第 9 节检查号为 25,32,34,43。证据状态为补证。

多个衍生 logger 共享诊断状态，已有写入错误只报一次验证；错误发生前后的字段隔离可联测。 [log/json.go:422](../../../log/json.go#L422)

[复审报告](../../../docs/evidence/stable-quality-review-a074eadb.md)

## 验收与验证

- [x] 日志字段各属自身，诊断固定一次。
- [x] 记录运行结果。若已有用例覆盖目标，记录用例及相关断言。

```sh
go test -race -timeout 60s ./log
```

## 实施证据

- [TestJSONScopedFieldsStayIndependentAfterWriteFailure](../../../log/json_test.go#L923)

[批次验证与基准](../review-a074eadb/README.md)
