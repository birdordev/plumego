# Card 2050 — 文件克隆指针时间戳独立

Milestone: 稳定模块第 9 节复审
Recipe: specs/change-recipes/add-acceptance-tests.yaml
Context Package: implementation
Priority: P3
State: done
Primary Module: store/file
Owned Files: `store/file/coverage_test.go`
Depends On: 无

Goal: 文件克隆指针时间戳独立
Scope: 分别改写 clone 时间戳，并整理 nil 和显式零时间的输出。
Non-goals: 范围限于本卡涉及的契约，保持公开签名和依赖。
Files: `store/file/coverage_test.go`。
Tests: 原指针值不变，nil 和显式零可以区分。
Docs Sync: 核对新增验收与已有模块说明一致，记录证据和限制。
Done Definition: 完成以下验收，记录命令、结果与剩余限制。
Outcome: 文件快照时间指针独立，显式零时间在克隆后保留，可与 nil 区分。

## 证据与影响

证据状态为补证。第 9 节检查号为 16,34,41,45。 [质量规则第 9 节](../../../docs/operations/agent-code-quality-rules.md#L267)

File.Clone 独立复制可选时间戳。对指针字段做和 Metadata 不同的所有权断言。 [store/file/types.go:162](../../../store/file/types.go#L162)

[复审报告](../../../docs/evidence/stable-quality-review-c3a77410.md)

## 验收与验证

- [x] 原指针值不变，nil 和显式零可以区分。
- [x] 已有测试覆盖相同触发和断言时可直接复用，记录具体用例。

```sh
go test -race -timeout 60s ./store/file
```

## 实施证据

- [TestFileClonesOwnTimePointersAndMetadataAcrossViews](../../../store/file/coverage_test.go#L310)
