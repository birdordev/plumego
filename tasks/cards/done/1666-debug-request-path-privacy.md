# Card 1666 — 验证调试路径元数据的最小公开范围

Milestone: 稳定模块质量复审
Recipe: specs/change-recipes/add-acceptance-tests.yaml
Context Package: implementation
Priority: P2
State: done
Primary Module: middleware
Owned Files: `middleware/debug/debug_errors.go`, `middleware/debug/debug_errors_test.go`, `docs/modules/middleware/README.md`
Depends On: 无

Goal: 验证调试路径元数据的最小公开范围。
Scope: 检查调用链中的路由模板和未匹配错误，仅用非敏感占位输入。
Non-goals: 维持公开签名和依赖；涉及现有兼容契约的调整须先核对调用边界及迁移影响。
Files: `middleware/debug/debug_errors.go`, `middleware/debug/debug_errors_test.go`, `docs/modules/middleware/README.md`。
Tests: 文档在匹配和未匹配时说明字段来源并验证共存的 request_id。
Docs Sync: 对照原契约核验，若证据要求修订公开行为，先更新实施范围。
Done Definition: 完成测试与验收记录，并标明剩余取证范围。
Outcome: 调试请求路径已存在路由模板时采用模板，其余沿用 URL 路径。两个场景都保留 request_id。

## 证据

检查号为 18,25,27,39，状态为补证。

查询已经改为存在标志；路径仍用于调试定位。是否能用已匹配模板取代原路径取决于调用约定。 [middleware/debug/debug_errors.go:369](../../../middleware/debug/debug_errors.go#L369)

[复审报告](../../../docs/evidence/stable-quality-review-09a7f5d3.md)

## 验收与验证

- [x] 文档在匹配和未匹配时说明字段来源并验证共存的 request_id。
- [x] 记录验证命令、状态和剩余范围。若使用既有用例满足验收，记录对应证据。

```sh
go test -race -timeout 60s ./middleware/debug
```

## 实施证据

[TestDebugRequestDetailsUseAvailableRoutePattern](../../../middleware/debug/debug_errors_test.go#L1175)
