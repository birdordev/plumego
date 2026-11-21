# Card 1975 — 核对请求标识固定宽度字段的往返

Milestone: 稳定模块质量复审
Recipe: specs/change-recipes/add-acceptance-tests.yaml
Context Package: implementation
Priority: P3
State: done
Primary Module: middleware/requestid
Owned Files: `middleware/requestid/request_id_generation_test.go`
Depends On: 无

Goal: 三个字段与边界值精确一致，不引入新格式约定。
Scope: 用本包夹具生成指定字段组合，并通过公开 Decode 解回。
Non-goals: 维持稳定公开签名与标准库依赖，将验证限于本卡范围。
Files: `middleware/requestid/request_id_generation_test.go`。
Tests: 三个字段与边界值精确一致，不引入新格式约定。
Docs Sync: 记录核验结论；只在现有契约文字需要澄清时另提文档变更。
Done Definition: 完成下列验收，记录验证命令、状态及剩余限制。
Outcome: 固定宽度标识可往返解码零值、小值与最大打包值。

## 证据与影响

第 9 节检查号为 6,25,39,45，当前证据状态为补证。 [质量规则第 9 节](../../../docs/operations/agent-code-quality-rules.md#L267)

生成器对时间、随机值、序列分别补位再拼接。零值或小数值的字段宽度需要直接核对。 [middleware/requestid/generator.go:191](../../../middleware/requestid/generator.go#L191)

[复审报告](../../../docs/evidence/stable-quality-review-999d5e4d.md)

## 验收与验证

- [x] 三个字段与边界值精确一致，不引入新格式约定。
- [x] 记录挂载入口、具体断言和结果。已有测试能覆盖时复用并注明。

```sh
go test -race -timeout 60s ./middleware/requestid
```

## 实施证据

- [TestDecodeRequestIDPaddedFieldRoundTrip](../../../middleware/requestid/request_id_generation_test.go#L245)
