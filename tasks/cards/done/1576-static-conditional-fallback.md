# Card 1576 — 不可 seek 静态文件的条件请求语义不一致

Milestone: 本轮质量审查
Recipe: specs/change-recipes/fix-bug.yaml
Context Package: implementation
Priority: P2
State: done
Primary Module: router
Owned Files: `router/static.go`, `router/static_test.go`, `docs/modules/router/README.md`, `website/src/generated/modules.ts`
Depends On: 无

Goal: 不可 seek 静态文件的条件请求语义不一致。
Scope: 明确 fallback 的条件契约，优先补与文件时间有关的无正文响应，或文档明确 fallback 的有限能力并加断言。
Non-goals: 保留现有公开签名和依赖。涉及未授权的行为或兼容契约调整时，先完成调用方核对与兼容决策。
Files: `router/static.go`, `router/static_test.go`, `docs/modules/router/README.md`, `website/src/generated/modules.ts`。
Tests: 用同数据的可 seek 与不可 seek 两种文件测试日期条件和 HEAD，无需网络文件系统。
Docs Sync: 核对 `docs/modules/router/README.md`。 修改模块说明后检查网站生成差异，并在同一交付中保留相应生成文件。
Done Definition: 完成下列验收和验证，记录证据与剩余风险。
Outcome: 保留顺序读取的兼容行为，文档明确不可 seek 时忽略 Range 和条件头；真实入口对照 GET 和 HEAD。

## 证据与影响

- 检查号：6,17,24,37。证据状态：源码确认。
- 可 seek 分支委托 ServeContent，原始复制分支只设置长度和 MIME，未处理日期条件。使用不可 seek 文件系统时，已有条件请求会按完整传输处理。 [router/static.go:280](../../../router/static.go#L280)
- [审查报告](../../../docs/evidence/stable-quality-review-dee7aa0f.md)

## 验收与验证

- [x] 用同数据的可 seek 与不可 seek 两种文件测试日期条件和 HEAD，无需网络文件系统。
- [x] 运行以下命令并记录结果、失败路径和未验证范围。修改 Go 文件前完成仓库预检，验证前先执行仓库格式化。

```sh
go test -race -timeout 60s ./router
make website-sync
make website-check-generated
```

## 实施证据

[TestStaticFSSequentialConditionalRequestContract](../../../router/static_test.go#L559) · [TestStaticFSSequentialAndSeekable](../../../router/static_test.go#L510)
