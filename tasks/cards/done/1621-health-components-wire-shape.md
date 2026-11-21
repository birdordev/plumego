# Card 1621 — 记录就绪组件表的空值语义

Milestone: 本轮质量审查
Recipe: specs/change-recipes/add-acceptance-tests.yaml
Context Package: implementation
Priority: P3
State: done
Primary Module: health
Owned Files: `health/json_contract_test.go`, `docs/modules/health/README.md`, `website/src/generated/modules.ts`
Depends On: 无

Goal: 记录就绪组件表的空值语义。
Scope: 补齐两种空形态在 JSON 与 Go 对象中的区别。
Non-goals: 保留现有公开签名、依赖与模块边界。测试若证实新的缺陷，先记录证据再单独安排修复。
Files: `health/json_contract_test.go`, `docs/modules/health/README.md`, `website/src/generated/modules.ts`。
Tests: 两个空值均省略 JSON 字段，克隆保持原有 Go 表示，有内容时可修改返回表而不影响原表。
Docs Sync: 核对 `docs/modules/health/README.md`。 修改模块说明后检查网站生成差异，并在同一交付中保留相应生成文件。
Done Definition: 完成下列验收和验证，记录证据与剩余风险。
Outcome: 文档和测试记录 nil 与空表都省略 JSON 字段，Clone 保留 Go 形态并复制非空表。

## 证据与影响

- 检查号：6,16,34,42。证据状态：补证。
- 组件表使用 omitempty，Clone 则保留 nil 与空表的形态。 [health/readiness.go:14](../../../health/readiness.go#L14)
- [审查报告](../../../docs/evidence/stable-quality-review-dee7aa0f.md)

## 验收与验证

- [x] 两个空值均省略 JSON 字段，克隆保持原有 Go 表示，有内容时可修改返回表而不影响原表。
- [x] 运行以下命令并记录结果、失败路径和未验证范围。修改 Go 文件前完成仓库预检，验证前先执行仓库格式化。

```sh
go test -race -timeout 60s ./health
make website-sync
make website-check-generated
```

## 实施证据

[TestReadinessEmptyComponentsWireAndClone](../../../health/json_contract_test.go#L187)
