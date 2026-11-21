# Card 2057 — 文本多层字段覆盖优先级

Milestone: 稳定模块第 9 节复审
Recipe: specs/change-recipes/add-acceptance-tests.yaml
Context Package: implementation
Priority: P3
State: done
Primary Module: log
Owned Files: `log/fields_test.go`
Depends On: 无

Goal: 文本多层字段覆盖优先级
Scope: 父、子和每次调用各以普通值覆盖同一字段。
Non-goals: 范围限于本卡涉及的契约，保持公开签名和依赖。
Files: `log/fields_test.go`。
Tests: 每条输出只有一个目标字段，最终按现有优先级取值，父字段不被污染。
Docs Sync: 核对新增验收与已有模块说明一致，记录证据和限制。
Done Definition: 完成以下验收，记录命令、结果与剩余限制。
Outcome: 文本日志每条只保留一个同名字段，调用、子级及父级各自取值符合覆盖规则。

## 证据与影响

证据状态为补证。第 9 节检查号为 6,34,43。 [质量规则第 9 节](../../../docs/operations/agent-code-quality-rules.md#L267)

调用字段和继承字段共同参与输出。需要从文本公开入口核验三层同名覆盖和独立性。 [log/logger.go:185](../../../log/logger.go#L185)

[复审报告](../../../docs/evidence/stable-quality-review-c3a77410.md)

## 验收与验证

- [x] 每条输出只有一个目标字段，最终按现有优先级取值，父字段不被污染。
- [x] 已有测试覆盖相同触发和断言时可直接复用，记录具体用例。

```sh
go test -race -timeout 60s ./log
```

## 实施证据

- [TestTextLoggerFieldPrecedenceAcrossParentChildAndCall](../../../log/fields_test.go#L90)
