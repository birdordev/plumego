# Card 1943 — 核对组件健康快照的 Dependencies 容量和宿主转发边界

Milestone: 稳定模块质量复审
Recipe: specs/change-recipes/add-acceptance-tests.yaml
Context Package: implementation
Priority: P2
State: done
Primary Module: health
Owned Files: `health/health_test.go`
Depends On: 无

Goal: 源和第二份保持，状态和 Enabled 不随切片变化。
Scope: 构造有额外容量的普通依赖列表，生成两份组件快照，对第一份追加和改写。
Non-goals: 维持稳定公开签名与标准库依赖，将验证限于本卡范围。
Files: `health/health_test.go`。
Tests: 源和第二份保持，状态和 Enabled 不随切片变化。
Docs Sync: 记录核验结论；只在现有契约文字需要澄清时另提文档变更。
Done Definition: 完成下列验收，记录验证命令、状态及剩余限制。
Outcome: 组件健康 Clone 的依赖 slice 与源及另一副本独立，追加不会写入源的备用容量。

## 证据与影响

第 9 节检查号为 5,16,29,34,42。证据状态为补证。

组件 Clone 复制依赖列表。需要单独覆盖依赖 slice 有备用容量、读回后 append 以及 Enabled 取值。 [health/core.go:55](../../../health/core.go#L55)

[复审报告](../../../docs/evidence/stable-quality-review-2b94cf86.md)

## 验收与验证

- [x] 源和第二份保持，状态和 Enabled 不随切片变化。
- [x] 记录挂载入口、具体断言和结果。已有测试能覆盖时复用并注明。

```sh
go test -race -timeout 60s ./health
```

## 实施证据

- [TestComponentCloneKeepsDependencyAppendAndEnabledIndependent](../../../health/health_test.go#L217)

[批次验证与基准](../review-2b94cf86/README.md)
