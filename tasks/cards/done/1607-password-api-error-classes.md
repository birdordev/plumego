# Card 1607 — 补证密码校验公开错误分类

Milestone: 本轮质量审查
Recipe: specs/change-recipes/add-acceptance-tests.yaml
Context Package: implementation
Priority: P2
State: done
Primary Module: security
Owned Files: `security/password/password_test.go`
Depends On: 无

Goal: 补证密码校验公开错误分类。
Scope: 用人工格式样本及测试密码走公开接口。
Non-goals: 保留现有公开签名、依赖与模块边界。测试若证实新的缺陷，先记录证据再单独安排修复。
Files: `security/password/password_test.go`。
Tests: 分类与文档一致，输出中无测试密码和序列化散列内容。
Docs Sync: 本卡按现有契约验证。若取证表明契约需调整，先更新后续卡范围。
Done Definition: 完成下列验收和验证，记录证据与剩余风险。
Outcome: 使用测试占位数据校验错误分类和无输入材料输出。

## 证据与影响

- 检查号：22,25,40,46。证据状态：补证。
- 密码校验区分格式、成本和校验结果，错误链需保持可分类及无凭据。 [security/password/password.go:264](../../../security/password/password.go#L264)
- [审查报告](../../../docs/evidence/stable-quality-review-dee7aa0f.md)

## 验收与验证

- [x] 分类与文档一致，输出中无测试密码和序列化散列内容。
- [x] 运行以下命令并记录结果、失败路径和未验证范围。修改 Go 文件前完成仓库预检，验证前先执行仓库格式化。

```sh
go test -race -timeout 60s ./security/password
```

## 实施证据

[TestCheckPasswordErrorsOmitInputMaterial](../../../security/password/password_test.go#L200) · [TestCheckPasswordInvalidHashFormats](../../../security/password/password_test.go#L148)
