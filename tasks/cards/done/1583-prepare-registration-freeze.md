# Card 1583 — 补证构建失败后的公开准备状态

Milestone: 本轮质量审查
Recipe: specs/change-recipes/add-acceptance-tests.yaml
Context Package: implementation
Priority: P2
State: done
Primary Module: core
Owned Files: `core/core_public_test.go`
Depends On: 1570（准备状态变更后复核）

Goal: 补证构建失败后的公开准备状态。
Scope: 扩充已有准备失败测试，记录当前可见状态和可重试动作。
Non-goals: 保留现有公开签名、依赖与模块边界。测试若证实新的缺陷，先记录证据再单独安排修复。
Files: `core/core_public_test.go`。
Tests: 断言准备失败、追加注册、再准备成功三步的公开返回值，不依赖内部字段。
Docs Sync: 本卡按现有契约验证。若取证表明契约需调整，先更新后续卡范围。
Done Definition: 完成下列验收和验证，记录证据与剩余风险。
Outcome: 公开回归验证组装失败、追加注册被拒以及随后 Prepare 成功。

## 证据与影响

- 检查号：6,28,31,36,46。证据状态：补证。
- 构建器返回 nil 后处理器未发布，但注册已冻结，接口状态仍使用最近成功阶段。 [core/http_handler.go:31](../../../core/http_handler.go#L31)
- [审查报告](../../../docs/evidence/stable-quality-review-dee7aa0f.md)

## 验收与验证

- [x] 断言准备失败、追加注册、再准备成功三步的公开返回值，不依赖内部字段。
- [x] 运行以下命令并记录结果、失败路径和未验证范围。修改 Go 文件前完成仓库预检，验证前先执行仓库格式化。

```sh
go test -race -timeout 60s ./core
```

## 实施证据

[TestPrepareRejectsNilFromIntermediateBuilder](../../../core/core_public_test.go#L22)
