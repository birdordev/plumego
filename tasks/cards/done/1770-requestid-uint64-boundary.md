# Card 1770 — 验证关联 ID 诊断解码的整数边界

Milestone: 稳定模块质量复审
Recipe: specs/change-recipes/add-acceptance-tests.yaml
Context Package: implementation
Priority: P2
State: done
Primary Module: middleware
Owned Files: `middleware/requestid/request_id_generation_test.go`
Depends On: 无

Goal: 最大值可解码，溢出和长度错误同类拒绝。
Scope: 构造最大值的标准宽度编码和字母表越界的普通占位输入。
Non-goals: 维持稳定公开签名与标准库依赖，将验证限于本卡范围。
Files: `middleware/requestid/request_id_generation_test.go`。
Tests: 最大值可解码，溢出和长度错误同类拒绝。
Docs Sync: 复核本卡涉及的已公开约定。
Done Definition: 完成下列验收，记录验证命令、状态及剩余限制。
Outcome: 关联 ID 最大整数边界可逆，溢出、非法字母与错误长度统一拒绝。

## 证据与影响

第 9 节检查号为 6,18,22,39。证据状态为补证。

解码乘加显式检查上界，随机生成的 ID 不保证命中精确边界。 [middleware/requestid/generator.go:222](../../../middleware/requestid/generator.go#L222)

[复审报告](../../../docs/evidence/stable-quality-review-d224acbc.md)

## 验收与验证

- [x] 最大值可解码，溢出和长度错误同类拒绝。
- [x] 记录运行结果。若已有用例覆盖目标，记录用例及相关断言。

```sh
go test -race -timeout 60s ./middleware/requestid
```

## 实施证据

- [TestDecodeRequestIDRejectsOverflow](../../../middleware/requestid/request_id_generation_test.go#L11)

[批次验证与基准](../review-d224acbc/README.md)
