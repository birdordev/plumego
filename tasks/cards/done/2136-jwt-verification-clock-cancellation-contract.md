# Card 2136 — 确认 JWT 验证时间判定期间的取消口径

Milestone: 稳定模块第 9 节复审
Recipe: specs/change-recipes/analysis-only.yaml
Context Package: review
Priority: P2
State: done
Primary Module: security/jwt
Owned Files: `security/jwt/jwt_verify_test.go`
Depends On: 无

Goal: 确认 JWT 验证时间判定期间的取消口径。
Scope: 用已知有效的合成 token 与受控时钟核对取消时点，保留既有失效分类。根据结果记录返回契约。
Non-goals: 本卡提交取证结果。后续行为变更由独立修复卡执行。
Files: `security/jwt/jwt_verify_test.go`
Tests: 只记录分类和 claims 是否为空。覆盖开始前、时钟取样时和成功返回之后，核对过期和正常样本。
Docs Sync: 核对既有文档与验收结果一致，记录需要后续同步的位置。
Done Definition: 记录命令、结果、证据限制和是否需要独立修复卡。
Outcome: 合成有效凭据验证在最后的取消取样通过后，时钟阶段的取消允许当前纯计算完成；已在既有用例验证预取消与过期分类。仅记录是否有 claims 和错误分类。保留现有语义。

## 证据与影响

证据状态为待补证。第 9 节检查号为 6,14,26,31,40,46。已覆盖验证开始前的取消。验签之后到语义检查和返回之间的取消优先级没有同样的公开入口矩阵。 [security/jwt/verify.go:11](../../../security/jwt/verify.go#L11) [security/jwt/verify.go:24](../../../security/jwt/verify.go#L24) [security/jwt/jwt_verify_test.go:319](../../../security/jwt/jwt_verify_test.go#L319)

[复审报告](../../../docs/evidence/stable-quality-review-6e8d53f9.md)

## 验收与验证

- [x] 只记录分类和 claims 是否为空。覆盖开始前、时钟取样时和成功返回之后，核对过期和正常样本。
- [x] 记录命令、结果、证据限制和是否需要独立修复卡。

```sh
go test -race -timeout 60s ./security/jwt
```
