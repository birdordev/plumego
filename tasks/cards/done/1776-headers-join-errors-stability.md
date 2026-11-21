# Card 1776 — 核对多项头策略配置失败的诊断分类

Milestone: 稳定模块质量复审
Recipe: specs/change-recipes/add-acceptance-tests.yaml
Context Package: implementation
Priority: P2
State: done
Primary Module: security
Owned Files: `security/headers/headers_test.go`
Depends On: 无

Goal: 不要求排序，失败不部分应用。
Scope: 使用不含凭据的配置桩，断言固定类别和原头保留。
Non-goals: 维持稳定公开签名与标准库依赖，将验证限于本卡范围。
Files: `security/headers/headers_test.go`。
Tests: 不要求排序，失败不部分应用。
Docs Sync: 复核本卡涉及的已公开约定。
Done Definition: 完成下列验收，记录验证命令、状态及剩余限制。
Outcome: 多项失败可共同报告，调用方按统一错误类别与规范头名判断，已有头未变化。

## 证据与影响

第 9 节检查号为 14,22,25,40。证据状态为补证。

Validate 可 join 多项验证错误，map 迭代顺序并非稳定合同。调用方应依赖分类与规范名字。 [security/headers/headers.go:305](../../../security/headers/headers.go#L305)

[复审报告](../../../docs/evidence/stable-quality-review-d224acbc.md)

## 验收与验证

- [x] 不要求排序，失败不部分应用。
- [x] 记录运行结果。若已有用例覆盖目标，记录用例及相关断言。

```sh
go test -race -timeout 60s ./security/headers
```

## 实施证据

- [TestMultipleInvalidPolicyFieldsPreserveClassificationAndHeaders](../../../security/headers/headers_test.go#L718)

[批次验证与基准](../review-d224acbc/README.md)
