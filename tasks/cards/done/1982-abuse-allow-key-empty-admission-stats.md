# Card 1982 — 核对空键拒绝的桶计量

Milestone: 稳定模块质量复审
Recipe: specs/change-recipes/add-acceptance-tests.yaml
Context Package: implementation
Priority: P2
State: done
Primary Module: security/abuse
Owned Files: `security/abuse/limiter_test.go`
Depends On: 无

Goal: 无有效桶产生，随后正常输入仍可使用全部初始容量。
Scope: 使用短生命周期 Limiter，从公开接口读取校验前后指标。
Non-goals: 维持稳定公开签名与标准库依赖，将验证限于本卡范围。
Files: `security/abuse/limiter_test.go`。
Tests: 无有效桶产生，随后正常输入仍可使用全部初始容量。
Docs Sync: 记录核验结论；只在现有契约文字需要澄清时另提文档变更。
Done Definition: 完成下列验收，记录验证命令、状态及剩余限制。
Outcome: 空键增加拒绝计数而不占桶，随后普通键使用唯一初始容量且不触发驱逐。

## 证据与影响

第 9 节检查号为 28,34,35,40,44，当前证据状态为补证。 [质量规则第 9 节](../../../docs/operations/agent-code-quality-rules.md#L267)

AllowKey 对空键有独立输入判断。现有空键测试检查拒绝。桶容量与指标是否变化应再与正常单键对照。 [security/abuse/limiter.go:443](../../../security/abuse/limiter.go#L443)

[复审报告](../../../docs/evidence/stable-quality-review-999d5e4d.md)

## 验收与验证

- [x] 无有效桶产生，随后正常输入仍可使用全部初始容量。
- [x] 记录挂载入口、具体断言和结果。已有测试能覆盖时复用并注明。

```sh
go test -race -timeout 60s ./security/abuse
```

## 实施证据

- [TestEmptyKeyDoesNotConsumeBucketCapacity](../../../security/abuse/limiter_test.go#L1034)
