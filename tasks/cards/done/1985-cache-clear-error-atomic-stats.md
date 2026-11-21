# Card 1985 — 核对取消的 Clear 对条目数和内存计量无副作用

Milestone: 稳定模块质量复审
Recipe: specs/change-recipes/add-acceptance-tests.yaml
Context Package: implementation
Priority: P2
State: done
Primary Module: store/cache
Owned Files: `store/cache/state_regression_test.go`
Depends On: 无

Goal: 失败时原计数保持，成功时按当前契约重置。
Scope: 先记录条目数和内存，预取消 Clear，随后正常 Clear 并读取统计。
Non-goals: 维持稳定公开签名与标准库依赖，将验证限于本卡范围。
Files: `store/cache/state_regression_test.go`。
Tests: 失败时原计数保持，成功时按当前契约重置。
Docs Sync: 记录核验结论；只在现有契约文字需要澄清时另提文档变更。
Done Definition: 完成下列验收，记录验证命令、状态及剩余限制。
Outcome: 预取消 Clear 保留条目数和字节计量，成功 Clear 后归零。

## 证据与影响

第 9 节检查号为 22,26,34,41，当前证据状态为补证。 [质量规则第 9 节](../../../docs/operations/agent-code-quality-rules.md#L267)

Clear 与条目数、内存归零在同一路径，但上下文和关闭检查先发生。取消失败后是否保持旧统计需要独立锁定。 [store/cache/memory.go:316](../../../store/cache/memory.go#L316)

[复审报告](../../../docs/evidence/stable-quality-review-999d5e4d.md)

## 验收与验证

- [x] 失败时原计数保持，成功时按当前契约重置。
- [x] 记录挂载入口、具体断言和结果。已有测试能覆盖时复用并注明。

```sh
go test -race -timeout 60s ./store/cache
```

## 实施证据

- [TestCanceledCacheClearPreservesEntriesAndBytes](../../../store/cache/state_regression_test.go#L594)
