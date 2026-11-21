# Card 1591 — 补证错误详情编码失败叠加短写

Milestone: 本轮质量审查
Recipe: specs/change-recipes/add-acceptance-tests.yaml
Context Package: implementation
Priority: P2
State: done
Primary Module: contract
Owned Files: `contract/response_test.go`
Depends On: 无

Goal: 补证错误详情编码失败叠加短写。
Scope: 复用现有短 writer 和可控的详情编码错误。
Non-goals: 保留现有公开签名、依赖与模块边界。测试若证实新的缺陷，先记录证据再单独安排修复。
Files: `contract/response_test.go`。
Tests: 断言 errors.Is 对两个原因都成立，最终状态只提交一次。
Docs Sync: 本卡按现有契约验证。若取证表明契约需调整，先更新后续卡范围。
Done Definition: 完成下列验收和验证，记录证据与剩余风险。
Outcome: 公开 WriteError 验证详情编码错误和短写都能分类，终态只提交一次。

## 证据与影响

- 检查号：21,22,23,38,46。证据状态：补证。
- 错误详情失败会回退，短写也有单独测试；二者组合通过 errors.Join 返回。 [contract/response.go:141](../../../contract/response.go#L141)
- [审查报告](../../../docs/evidence/stable-quality-review-dee7aa0f.md)

## 验收与验证

- [x] 断言 errors.Is 对两个原因都成立，最终状态只提交一次。
- [x] 运行以下命令并记录结果、失败路径和未验证范围。修改 Go 文件前完成仓库预检，验证前先执行仓库格式化。

```sh
go test -race -timeout 60s ./contract
```

## 实施证据

[TestWriteErrorJoinsEncodingFailureWithShortWrite](../../../contract/response_test.go#L693)
