# Card 1606 — 核对 Principal 多读者共享边界

Milestone: 本轮质量审查
Recipe: specs/change-recipes/add-acceptance-tests.yaml
Context Package: implementation
Priority: P2
State: done
Primary Module: security
Owned Files: `security/authn/authn_test.go`
Depends On: 无

Goal: 核对 Principal 多读者共享边界。
Scope: 复用现有认证主体夹具，增加两个消费者的隔离断言。
Non-goals: 保留现有公开签名、依赖与模块边界。测试若证实新的缺陷，先记录证据再单独安排修复。
Files: `security/authn/authn_test.go`。
Tests: 原始值及第一个读者变更不改变第二个读者所见的受支持字段。
Docs Sync: 本卡按现有契约验证。若取证表明契约需调整，先更新后续卡范围。
Done Definition: 完成下列验收和验证，记录证据与剩余风险。
Outcome: 既有回归已覆盖源 Principal 与两个读取者之间的 slice 和 map 隔离。

## 证据与影响

- 检查号：16,27,34,40。证据状态：补证。
- 鉴权主体进入和离开上下文时使用克隆，声明切片和映射的复制层级需钉牢。 [security/authn/authn.go:68](../../../security/authn/authn.go#L68)
- [审查报告](../../../docs/evidence/stable-quality-review-dee7aa0f.md)

## 验收与验证

- [x] 原始值及第一个读者变更不改变第二个读者所见的受支持字段。
- [x] 运行以下命令并记录结果、失败路径和未验证范围。修改 Go 文件前完成仓库预检，验证前先执行仓库格式化。

```sh
go test -race -timeout 60s ./security/authn
```

## 实施证据

[TestPrincipalContextCopiesMutableFields](../../../security/authn/authn_test.go#L29)
