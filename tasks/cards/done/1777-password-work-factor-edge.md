# Card 1777 — 核对密码成本边界分类且不触发高成本计算

Milestone: 稳定模块质量复审
Recipe: specs/change-recipes/add-acceptance-tests.yaml
Context Package: implementation
Priority: P2
State: done
Primary Module: security
Owned Files: `security/password/password_test.go`
Depends On: 无

Goal: 非法成本在派生前返回，诊断不包含输入值。
Scope: 只做守卫值测试和普通成本样例，避免运行高成本派生。
Non-goals: 维持稳定公开签名与标准库依赖，将验证限于本卡范围。
Files: `security/password/password_test.go`。
Tests: 非法成本在派生前返回，诊断不包含输入值。
Docs Sync: 复核本卡涉及的已公开约定。
Done Definition: 完成下列验收，记录验证命令、状态及剩余限制。
Outcome: 构造和读取均在非法成本边界停止，读取错误保留两层分类，诊断无人工输入。

## 证据与影响

第 9 节检查号为 14,25,35,40。证据状态为补证。

成本可能来自公开 API 和已保存数据。边界分类需覆盖构造与读取路径。 [security/password/password.go:336](../../../security/password/password.go#L336)

[复审报告](../../../docs/evidence/stable-quality-review-d224acbc.md)

## 验收与验证

- [x] 非法成本在派生前返回，诊断不包含输入值。
- [x] 记录运行结果。若已有用例覆盖目标，记录用例及相关断言。

```sh
go test -race -timeout 60s ./security/password
```

## 实施证据

- [TestInvalidWorkFactorsRejectedBeforeDecodingStoredFields](../../../security/password/password_test.go#L225)

[批次验证与基准](../review-d224acbc/README.md)
