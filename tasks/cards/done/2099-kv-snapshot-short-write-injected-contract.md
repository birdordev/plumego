# Card 2099 — 快照注入短写返回值的假设

Milestone: 稳定模块第 9 节复审
Recipe: specs/change-recipes/analysis-only.yaml
Context Package: review
Priority: P2
State: done
Primary Module: store/kv
Owned Files: `store/kv/state_regression_test.go`
Depends On: 无

Goal: 快照注入短写返回值的假设
Scope: 用临时宿主验证短写的接口行为和现有替换测试，先决定是否应显式防御。
Non-goals: 范围限于本卡涉及的契约，保持公开签名和依赖。
Files: `store/kv/state_regression_test.go`。
Tests: 记录真实文件调用关系、受控结果及兼容判断。需要实现时另建独立卡。
Docs Sync: 核对新增验收与已有模块说明一致，记录证据和限制。
Done Definition: 完成以下验收，记录命令、结果与剩余限制。
Outcome: 完成分析。非标准测试 writer 成功返回 7/14 字节会发布截断文件，重开报 ErrCorruptState。正式路径使用遵守短写必有错误契约的 os.File，未改变实现。

## 证据与影响

证据状态为补证。第 9 节检查号为 22,29,30,31,41。 [质量规则第 9 节](../../../docs/operations/agent-code-quality-rules.md#L267)

真实 os.File 的短写返回非 nil 错误。内部窄接口只检查 err，在受控非标准 writer 中可能接纳短写，生产可达性未证实。 [store/kv/persistence.go:255](../../../store/kv/persistence.go#L255)

[复审报告](../../../docs/evidence/stable-quality-review-2f962d21.md)

## 验收与验证

- [x] 记录真实文件调用关系、受控结果及兼容判断。需要实现时另建独立卡。
- [x] 验收以记录观察结果和兼容性判断为准。

## 实施证据

把快照替身的 Write 限制为一半，并特意保留 nil 错误。replaceSnapshot 发布了 7/14 字节，重开返回 ErrCorruptState。正式调用者是 os.CreateTemp 返回的 *os.File，os.File.Write 保证短写同时返回错误。 临时探针通过，工作区内未新增对应实现。

```sh
go test -race -count=1 -v -overlay=/tmp/2f9-impl-kv-overlay.json ./store/kv -run '^TestReviewInjectedSnapshotShortWritePublishesPrefix$'
```
