# Card 1994 — 核对 Ready 与空原因的序列化组合

Milestone: 稳定模块质量复审
Recipe: specs/change-recipes/add-acceptance-tests.yaml
Context Package: implementation
Priority: P3
State: done
Primary Module: health
Owned Files: `health/health_test.go`
Depends On: 无

Goal: Ready 与理由的形状符合现有标签，模型不注入额外解释。
Scope: 用接口模型生成未就绪和已就绪两组，比较 Clone 和 JSON。
Non-goals: 维持稳定公开签名与标准库依赖，将验证限于本卡范围。
Files: `health/health_test.go`。
Tests: Ready 与理由的形状符合现有标签，模型不注入额外解释。
Docs Sync: 记录核验结论；只在现有契约文字需要澄清时另提文档变更。
Done Definition: 完成下列验收，记录验证命令、状态及剩余限制。
Outcome: 无理由的就绪和未就绪均保留 bool，但不生成 reason。

## 证据与影响

第 9 节检查号为 6,16,42,45，当前证据状态为补证。 [质量规则第 9 节](../../../docs/operations/agent-code-quality-rules.md#L267)

Readiness 提供 Ready、Reason 与 Timestamp，零值与调用者给定的空原因可以独立观察。 [health/readiness.go:18](../../../health/readiness.go#L18)

[复审报告](../../../docs/evidence/stable-quality-review-999d5e4d.md)

## 验收与验证

- [x] Ready 与理由的形状符合现有标签，模型不注入额外解释。
- [x] 记录挂载入口、具体断言和结果。已有测试能覆盖时复用并注明。

```sh
go test -race -timeout 60s ./health
```

## 实施证据

- [TestReadinessEmptyReasonOmittedRegardlessOfReady](../../../health/health_test.go#L231)
