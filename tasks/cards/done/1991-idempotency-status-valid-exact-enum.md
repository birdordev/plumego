# Card 1991 — 核对幂等状态枚举的精确值边界

Milestone: 稳定模块质量复审
Recipe: specs/change-recipes/add-acceptance-tests.yaml
Context Package: implementation
Priority: P3
State: done
Primary Module: store/idempotency
Owned Files: `store/idempotency/store_test.go`
Depends On: 无

Goal: 只接受契约列出的状态，两处分类一致。
Scope: 用表驱动比较 Status.Valid 与 ValidateRecord 的状态判断。
Non-goals: 维持稳定公开签名与标准库依赖，将验证限于本卡范围。
Files: `store/idempotency/store_test.go`。
Tests: 只接受契约列出的状态，两处分类一致。
Docs Sync: 记录核验结论；只在现有契约文字需要澄清时另提文档变更。
Done Definition: 完成下列验收，记录验证命令、状态及剩余限制。
Outcome: 只有两个精确状态可用于持久记录，零值、大小写变体和尾部空格均给出记录分类。

## 证据与影响

第 9 节检查号为 6,16,34,41，当前证据状态为补证。 [质量规则第 9 节](../../../docs/operations/agent-code-quality-rules.md#L267)

持久记录中的状态使用稳定字符串表示。零值、大小写不同值与全部已知成员可在验证入口统一核对。 [store/idempotency/store.go:51](../../../store/idempotency/store.go#L51)

[复审报告](../../../docs/evidence/stable-quality-review-999d5e4d.md)

## 验收与验证

- [x] 只接受契约列出的状态，两处分类一致。
- [x] 记录挂载入口、具体断言和结果。已有测试能覆盖时复用并注明。

```sh
go test -race -timeout 60s ./store/idempotency
```

## 实施证据

- [TestRecordStatusValidationMatchesExactEnumValues](../../../store/idempotency/store_test.go#L404)
