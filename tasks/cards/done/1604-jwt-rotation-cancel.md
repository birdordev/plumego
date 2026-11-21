# Card 1604 — 补证轮换等待的取消与后续可用性

Milestone: 本轮质量审查
Recipe: specs/change-recipes/add-acceptance-tests.yaml
Context Package: implementation
Priority: P2
State: done
Primary Module: security
Owned Files: `security/jwt/rotation_regression_test.go`
Depends On: 无

Goal: 补证轮换等待的取消与后续可用性。
Scope: 复用受控 keystore 和占位密钥对象，核对现有用例后补交错。
Non-goals: 保留现有公开签名、依赖与模块边界。测试若证实新的缺陷，先记录证据再单独安排修复。
Files: `security/jwt/rotation_regression_test.go`。
Tests: 等待中取消返回 context 错误；有效签发入口仍可使用；后继轮换可正常完成。
Docs Sync: 本卡按现有契约验证。若取证表明契约需调整，先更新后续卡范围。
Done Definition: 完成下列验收和验证，记录证据与剩余风险。
Outcome: 阻塞轮换期间取消后继请求，并验证当前轮换释放后签发和新轮换成功。

## 证据与影响

- 检查号：26,28,31,32,40。证据状态：补证。
- 轮换使用独占槽，阻塞和取消发生于调用外部持久化前后。 [security/jwt/keys.go:118](../../../security/jwt/keys.go#L118)
- [审查报告](../../../docs/evidence/stable-quality-review-dee7aa0f.md)

## 验收与验证

- [x] 等待中取消返回 context 错误；有效签发入口仍可使用；后继轮换可正常完成。
- [x] 运行以下命令并记录结果、失败路径和未验证范围。修改 Go 文件前完成仓库预检，验证前先执行仓库格式化。

```sh
go test -race -timeout 60s ./security/jwt
```

## 实施证据

[TestRotationWaitHonorsCancellation](../../../security/jwt/rotation_regression_test.go#L14)
