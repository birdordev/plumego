# Card 1594 — 补证详情拷贝的自定义值边界

Milestone: 本轮质量审查
Recipe: specs/change-recipes/add-acceptance-tests.yaml
Context Package: implementation
Priority: P2
State: done
Primary Module: contract
Owned Files: `contract/errors_test.go`
Depends On: 无

Goal: 补证详情拷贝的自定义值边界。
Scope: 以具名自定义值覆盖当前拷贝策略，按已经批准的契约记录行为。
Non-goals: 保留现有公开签名、依赖与模块边界。测试若证实新的缺陷，先记录证据再单独安排修复。
Files: `contract/errors_test.go`。
Tests: 普通可复制容器隔离，定制编码输出保真；明确调用者何时不能并发变更对象。
Docs Sync: 本卡按现有契约验证。若取证表明契约需调整，先更新后续卡范围。
Done Definition: 完成下列验收和验证，记录证据与剩余风险。
Outcome: 组合验证调用者所有的自定义编码对象与普通容器的快照隔离。

## 证据与影响

- 检查号：16,23,34,38。证据状态：补证。
- JSON 容器被复制，自定义 marshal 值可能沿用对象所有权。 [contract/errors.go:240](../../../contract/errors.go#L240)
- [审查报告](../../../docs/evidence/stable-quality-review-dee7aa0f.md)

## 验收与验证

- [x] 普通可复制容器隔离，定制编码输出保真；明确调用者何时不能并发变更对象。
- [x] 运行以下命令并记录结果、失败路径和未验证范围。修改 Go 文件前完成仓库预检，验证前先执行仓库格式化。

```sh
go test -race -timeout 60s ./contract
```

## 实施证据

[TestErrorDetailsPreserveCustomEncodingAndCopyContainers](../../../contract/errors_test.go#L1196)
