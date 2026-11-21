# Card 1585 — 补证 TLS 配置失败恢复后再准备

Milestone: 本轮质量审查
Recipe: specs/change-recipes/add-acceptance-tests.yaml
Context Package: implementation
Priority: P2
State: done
Primary Module: core
Owned Files: `core/core_public_test.go`
Depends On: 无

Goal: 补证 TLS 配置失败恢复后再准备。
Scope: 用临时目录和测试证书扩展已有 TLS 测试。
Non-goals: 保留现有公开签名、依赖与模块边界。测试若证实新的缺陷，先记录证据再单独安排修复。
Files: `core/core_public_test.go`。
Tests: 首次缺文件失败且仍可注册，补齐文件后只发布一个服务器，随后可 Shutdown。
Docs Sync: 本卡按现有契约验证。若取证表明契约需调整，先更新后续卡范围。
Done Definition: 完成下列验收和验证，记录证据与剩余风险。
Outcome: 缺证书时保持注册可用，补文件后重复 Prepare 返回同一个服务器并可 Shutdown。

## 证据与影响

- 检查号：14,15,28,36。证据状态：补证。
- 证书加载发生于 handler 冻结之前，恢复相同路径文件后的状态转换需公开入口验证。 [core/http_handler.go:69](../../../core/http_handler.go#L69)
- [审查报告](../../../docs/evidence/stable-quality-review-dee7aa0f.md)

## 验收与验证

- [x] 首次缺文件失败且仍可注册，补齐文件后只发布一个服务器，随后可 Shutdown。
- [x] 运行以下命令并记录结果、失败路径和未验证范围。修改 Go 文件前完成仓库预检，验证前先执行仓库格式化。

```sh
go test -race -timeout 60s ./core
```

## 实施证据

[TestPublicPrepareRetriesAfterTLSFilesAppear](../../../core/core_public_test.go#L501)
