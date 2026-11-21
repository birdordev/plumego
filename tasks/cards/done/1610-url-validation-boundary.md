# Card 1610 — 锁定 URL 字面校验与调用方责任

Milestone: 本轮质量审查
Recipe: specs/change-recipes/add-acceptance-tests.yaml
Context Package: implementation
Priority: P2
State: done
Primary Module: security
Owned Files: `security/input/input_test.go`, `docs/modules/security/README.md`, `website/src/generated/modules.ts`
Depends On: 无

Goal: 锁定 URL 字面校验与调用方责任。
Scope: 复用本地离线 URL 表驱动测试，核对模块说明。
Non-goals: 保留现有公开签名、依赖与模块边界。测试若证实新的缺陷，先记录证据再单独安排修复。
Files: `security/input/input_test.go`, `docs/modules/security/README.md`, `website/src/generated/modules.ts`。
Tests: 成功只代表当前字面校验条件，包含拒绝与适用范围的证据，无网络交互。
Docs Sync: 核对 `docs/modules/security/README.md`。 修改模块说明后检查网站生成差异，并在同一交付中保留相应生成文件。
Done Definition: 完成下列验收和验证，记录证据与剩余风险。
Outcome: 复用离线字面 URL 验证，确认模块与 GoDoc 将 DNS 决策留给调用方。

## 证据与影响

- 检查号：16,18,40,46。证据状态：补证。
- 文档将 DNS 与目标地址复核留给调用者。需要验证模型接口和例子表达同一边界。 [security/input/input.go:296](../../../security/input/input.go#L296)
- [审查报告](../../../docs/evidence/stable-quality-review-dee7aa0f.md)

## 验收与验证

- [x] 成功只代表当前字面校验条件，包含拒绝与适用范围的证据，无网络交互。
- [x] 运行以下命令并记录结果、失败路径和未验证范围。修改 Go 文件前完成仓库预检，验证前先执行仓库格式化。

```sh
go test -race -timeout 60s ./security/input
make website-sync
make website-check-generated
```

## 实施证据

[TestValidatePublicURL](../../../security/input/input_test.go#L142)
