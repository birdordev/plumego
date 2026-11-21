# Card 1641 — 手机号字数校验分配完整 rune 切片

Milestone: 稳定模块质量复审
Recipe: specs/change-recipes/fix-bug.yaml
Context Package: implementation
Priority: P2
State: done
Primary Module: security
Owned Files: `security/input/input.go`, `security/input/input_test.go`
Depends On: 无

Goal: 手机号字数校验分配完整 rune 切片。
Scope: 改用整数计数，超过位数上限立即拒绝，保留标点、开头加号和 Unicode 校验规则。
Non-goals: 维持公开签名和依赖；涉及现有兼容契约的调整须先核对调用边界及迁移影响。
Files: `security/input/input.go`, `security/input/input_test.go`。
Tests: 公开入口覆盖 7/15/16 位和格式边界，用基准比对同一组普通输入的 allocs/op。
Docs Sync: 对照原契约核验，若证据要求修订公开行为，先更新实施范围。
Done Definition: 完成测试与验收记录，并标明剩余取证范围。
Outcome: 手机号校验用整数计数，并在超过 15 位后返回。原先分隔符样例的 1 次分配降至 0 次。

## 证据

检查号为 9,12,35,48，状态为已复现。

ValidatePhone 只返回数字位数是否合法，却按输入字节数预分配 rune 切片。基础有效号码为 0 次分配，含多个分隔符的有效样例为 1 次。 [security/input/input.go:417](../../../security/input/input.go#L417)

[复审报告](../../../docs/evidence/stable-quality-review-09a7f5d3.md)

## 验收与验证

- [x] 公开入口覆盖 7/15/16 位和格式边界，用基准比对同一组普通输入的 allocs/op。
- [x] 记录验证命令、状态和剩余范围。若使用既有用例满足验收，记录对应证据。

```sh
go test -race -timeout 60s ./security/input
go test ./security/input -run '^$' -bench '^BenchmarkValidatePhoneDigitCount$' -benchmem -count=3
```

## 实施证据

[TestValidatePhoneDigitCountBounds](../../../security/input/input_test.go#L481) · [BenchmarkValidatePhoneDigitCount](../../../security/input/input_test.go#L496)
