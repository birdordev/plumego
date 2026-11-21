# Card 1886 — 核对 File 快照时间指针与多次 Clone 的归属

Milestone: 稳定模块质量复审
Recipe: specs/change-recipes/add-acceptance-tests.yaml
Context Package: implementation
Priority: P2
State: done
Primary Module: store
Owned Files: `store/file/coverage_test.go`
Depends On: 无

Goal: 原快照、两份副本及 nil 指针保有预期形状。
Scope: 设置单个合成时间源，克隆两次后分别变更可选日期和顶层元数据。
Non-goals: 维持稳定公开签名与标准库依赖，将验证限于本卡范围。
Files: `store/file/coverage_test.go`。
Tests: 原快照、两份副本及 nil 指针保有预期形状。
Docs Sync: 记录核验结论；只在现有契约文字需要澄清时另提文档变更。
Done Definition: 完成下列验收，记录验证命令、状态及剩余限制。
Outcome: File 的两份快照各自拥有时间指针和常用元数据，零值保持 nil。

## 证据与影响

第 9 节检查号为 6,29,34,41。证据状态为补证。

File.Clone 复制可选时间指针。两次 clone 的修改是否独立于原 File 和对方，需与元数据一起验证。 [store/file/types.go:162](../../../store/file/types.go#L162)

[复审报告](../../../docs/evidence/stable-quality-review-a8c2f28a.md)

## 验收与验证

- [x] 原快照、两份副本及 nil 指针保有预期形状。
- [x] 记录挂载入口、具体断言和结果。已有测试能覆盖时复用并注明。

```sh
go test -race -timeout 60s ./store/file
```

## 实施证据

- [TestFileClonesOwnTimePointersAndMetadataAcrossViews](../../../store/file/coverage_test.go#L310)

[批次验证与基准](../review-a8c2f28a/README.md)
