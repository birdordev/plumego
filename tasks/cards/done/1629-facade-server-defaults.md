# Card 1629 — 用公开入口核对门面配置说明

Milestone: 本轮质量审查
Recipe: specs/change-recipes/add-acceptance-tests.yaml
Context Package: implementation
Priority: P3
State: done
Primary Module: .
Owned Files: `plumego_test.go`, `docs/modules/plumego/README.md`
Depends On: 无

Goal: 用公开入口核对门面配置说明。
Scope: 复核默认值和传递行为，考虑将易变数字集中在所属模块文档。
Non-goals: 保留现有公开签名、依赖与模块边界。测试若证实新的缺陷，先记录证据再单独安排修复。
Files: `plumego_test.go`, `docs/modules/plumego/README.md`。
Tests: 验证默认构造与显式配置在公开 Server 入口的对应字段；根包导出集合保持不变。
Docs Sync: 核对 `docs/modules/plumego/README.md`。
Done Definition: 完成下列验收和验证，记录证据与剩余风险。
Outcome: GoDoc 以 core.DefaultConfig 为默认值来源，保留既有构造和预备服务器参数测试。

## 证据与影响

- 检查号：3,6,14,16,52。证据状态：补证。
- 门面 GoDoc 抄录了核心的固定默认值子集，容易跟随 core 变更漂移。 [plumego.go:37](../../../plumego.go#L37)
- [审查报告](../../../docs/evidence/stable-quality-review-dee7aa0f.md)

## 验收与验证

- [x] 验证默认构造与显式配置在公开 Server 入口的对应字段；根包导出集合保持不变。
- [x] 运行以下命令并记录结果、失败路径和未验证范围。修改 Go 文件前完成仓库预检，验证前先执行仓库格式化。

```sh
go test -race -timeout 60s .
make website-sync
make website-check-generated
```

## 实施证据

[TestDefaultConfigMatchesCoreDefaultConfig](../../../plumego_test.go#L104) · [TestNewWithConfigAppliesConfig](../../../plumego_test.go#L41) · [TestNewUsesDefaultAddr](../../../plumego_test.go#L139)
