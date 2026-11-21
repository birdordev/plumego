# Card 1637 — tracer 返回的 SpanID 在输出前没有统一验证

Milestone: 稳定模块质量复审
Recipe: specs/change-recipes/fix-bug.yaml
Context Package: implementation
Priority: P2
State: done
Primary Module: middleware
Owned Files: `middleware/internal/telemetry/helpers.go`, `middleware/internal/telemetry/helpers_test.go`, `docs/modules/middleware/README.md`
Depends On: 无

Goal: tracer 返回的 SpanID 在输出前没有统一验证。
Scope: 先将 span 输出与派生载体统一经已有 TraceContext 验证再发出，保留底层 span 的结束责任。
Non-goals: 维持公开签名和依赖；涉及现有兼容契约的调整须先核对调用边界及迁移影响。
Files: `middleware/internal/telemetry/helpers.go`, `middleware/internal/telemetry/helpers_test.go`, `docs/modules/middleware/README.md`。
Tests: 使用非敏感诊断占位值验证无效元数据被忽略、合法记录仍存在、span 结束一次。
Docs Sync: 复核 `docs/modules/middleware/README.md`，模块说明改动后核对生成文件差异。
Done Definition: 完成测试与验收记录，并标明剩余取证范围。
Outcome: 只从合法 trace 上下文或合法 span 元数据发布响应头。非法元数据仍归原 span 收尾。

## 证据

检查号为 18,25,27,39，状态为源码确认。

BeginTrace 仅在上下文字段回退时检查 Valid，直接从 span 读取的非空值可进响应头，其格式可能与上下文有效性不同。输出依赖注入的 tracer。 [middleware/internal/telemetry/helpers.go:116](../../../middleware/internal/telemetry/helpers.go#L116)

[复审报告](../../../docs/evidence/stable-quality-review-09a7f5d3.md)

## 验收与验证

- [x] 使用非敏感诊断占位值验证无效元数据被忽略、合法记录仍存在、span 结束一次。
- [x] 记录验证命令、状态和剩余范围。若使用既有用例满足验收，记录对应证据。

```sh
go test -race -timeout 60s ./middleware/internal/telemetry
make website-sync
make website-check-generated
```

## 实施证据

[TestBeginTracePublishesOnlyValidTraceIdentity](../../../middleware/internal/telemetry/helpers_test.go#L221)
