# Card 1640 — 数据库构造错误直接格式化注入 opener 的错误文本

Milestone: 稳定模块质量复审
Recipe: specs/change-recipes/fix-bug.yaml
Context Package: implementation
Priority: P2
State: done
Primary Module: store
Owned Files: `store/db/sql.go`, `store/db/sql_test.go`, `docs/modules/store/README.md`, `website/src/generated/modules.ts`
Depends On: 无

Goal: 数据库构造错误直接格式化注入 opener 的错误文本。
Scope: 保持已承诺的 errors.Is/As 分类；为默认对外字符串提供固定诊断，明确底层 cause 的消费者安全责任。改动前检查现有日志和分类调用。
Non-goals: 维持公开签名和依赖；涉及现有兼容契约的调整须先核对调用边界及迁移影响。
Files: `store/db/sql.go`, `store/db/sql_test.go`, `docs/modules/store/README.md`, `website/src/generated/modules.ts`。
Tests: 用人工标记错误验证默认文本及命名字段输出，确保分类和部分成功的清理错误仍可获取。
Docs Sync: 复核 `docs/modules/store/README.md`，模块说明改动后核对生成文件差异。
Done Definition: 完成测试与验收记录，并标明剩余取证范围。
Outcome: OpenWith 的默认驱动失败文本固定，主因和清理错误仍可用 errors.Is/As 分类。

## 证据

检查号为 22,25,30,41，状态为源码确认。

OpenWith 将自定义 opener 接收的底层错误直接作为可读文本拼接，数据库适配器错误可能携带连接配置片段。当前库只能保证自身参数校验直接报字段名。 [store/db/sql.go:166](../../../store/db/sql.go#L166)

[复审报告](../../../docs/evidence/stable-quality-review-09a7f5d3.md)

## 验收与验证

- [x] 用人工标记错误验证默认文本及命名字段输出，确保分类和部分成功的清理错误仍可获取。
- [x] 记录验证命令、状态和剩余范围。若使用既有用例满足验收，记录对应证据。

```sh
go test -race -timeout 60s ./store/db
make website-sync
make website-check-generated
```

## 实施证据

[TestOpenFailureUsesSafeDefaultDiagnostic](../../../store/db/sql_test.go#L1441) · [TestOpenWithClosesPartiallyOpenedPool](../../../store/db/sql_test.go#L1393)
