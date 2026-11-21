# Card 1667 — 核对超时替换对外层头部所有权的影响

Milestone: 稳定模块质量复审
Recipe: specs/change-recipes/add-acceptance-tests.yaml
Context Package: implementation
Priority: P2
State: done
Primary Module: middleware
Owned Files: `middleware/timeout/timeout.go`, `middleware/timeout/timeout_regression_test.go`, `docs/modules/middleware/README.md`
Depends On: 无

Goal: 核对超时替换对外层头部所有权的影响。
Scope: 向外层写入受控普通头并触发合成超时。
Non-goals: 维持公开签名和依赖；涉及现有兼容契约的调整须先核对调用边界及迁移影响。
Files: `middleware/timeout/timeout.go`, `middleware/timeout/timeout_regression_test.go`, `docs/modules/middleware/README.md`。
Tests: 结论需覆盖必须保留的追踪头以及与新响应相冲突的实体描述头。
Docs Sync: 对照原契约核验，若证据要求修订公开行为，先更新实施范围。
Done Definition: 完成测试与验收记录，并标明剩余取证范围。
Outcome: 超时生成错误时移除旧实体头，保留非表示相关的追踪和应用头。

## 证据

检查号为 21,23,24,39，状态为补证。

成功回放会复制响应头，超时走结构化错误。外层可能提供缓存、类型或长度相关字段。 [middleware/timeout/timeout.go:125](../../../middleware/timeout/timeout.go#L125)

[复审报告](../../../docs/evidence/stable-quality-review-09a7f5d3.md)

## 验收与验证

- [x] 结论需覆盖必须保留的追踪头以及与新响应相冲突的实体描述头。
- [x] 记录验证命令、状态和剩余范围。若使用既有用例满足验收，记录对应证据。

```sh
go test -race -timeout 60s ./middleware/timeout
```

## 实施证据

[TestTimeoutErrorPreservesMetadataAndDropsStaleRepresentation](../../../middleware/timeout/timeout_regression_test.go#L409)
