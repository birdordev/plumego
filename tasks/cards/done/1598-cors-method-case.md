# Card 1598 — 核对 CORS 预检方法大小写语义

Milestone: 本轮质量审查
Recipe: specs/change-recipes/add-acceptance-tests.yaml
Context Package: implementation
Priority: P2
State: done
Primary Module: middleware
Owned Files: `middleware/cors/cors.go`, `middleware/cors/cors_test.go`, `docs/modules/middleware/README.md`, `website/src/generated/modules.ts`
Depends On: 无

Goal: 核对 CORS 预检方法大小写语义。
Scope: 用本地预检测试先锁定普通、大小写不同和扩展方法的行为，再决定兼容策略。
Non-goals: 保留现有公开签名、依赖与模块边界。测试若证实新的缺陷，先记录证据再单独安排修复。
Files: `middleware/cors/cors.go`, `middleware/cors/cors_test.go`, `docs/modules/middleware/README.md`, `website/src/generated/modules.ts`。
Tests: 204 与下游透传的判定及 Allow-Methods 字面量有清晰断言。
Docs Sync: 核对 `docs/modules/middleware/README.md`。 修改模块说明后检查网站生成差异，并在同一交付中保留相应生成文件。
Done Definition: 完成下列验收和验证，记录证据与剩余风险。
Outcome: 用配置原始方法名做精确预检匹配；覆盖普通方法、自定义方法和大小写变体。

## 证据与影响

- 检查号：6,14,20,39。证据状态：补证。
- 匹配使用 EqualFold，发出的允许方法仍保留配置字面量，需核对自定义方法和部署方约定。 [middleware/cors/cors.go:174](../../../middleware/cors/cors.go#L174)
- [审查报告](../../../docs/evidence/stable-quality-review-dee7aa0f.md)

## 验收与验证

- [x] 204 与下游透传的判定及 Allow-Methods 字面量有清晰断言。
- [x] 运行以下命令并记录结果、失败路径和未验证范围。修改 Go 文件前完成仓库预检，验证前先执行仓库格式化。

```sh
go test -race -timeout 60s ./middleware/cors
make website-sync
make website-check-generated
```

## 实施证据

[TestPreflightUsesExactMethodName](../../../middleware/cors/cors_test.go#L615)
