# Card 1670 — 测量编译后安全头每次重复校验成本

Milestone: 稳定模块质量复审
Recipe: specs/change-recipes/add-acceptance-tests.yaml
Context Package: implementation
Priority: P2
State: done
Primary Module: security
Owned Files: `security/headers/headers_test.go`
Depends On: 无

Goal: 测量编译后安全头每次重复校验成本。
Scope: 基准比较默认与多 Additional 的安全配置，明确构造与每次响应的分配。
Non-goals: 维持公开签名和依赖；涉及现有兼容契约的调整须先核对调用边界及迁移影响。
Files: `security/headers/headers_test.go`。
Tests: 若后续缓存校验，仍需验证直接可变 Policy 和编译后的变更语义。
Docs Sync: 对照原契约核验，若证据要求修订公开行为，先更新实施范围。
Done Definition: 完成测试与验收记录，并标明剩余取证范围。
Outcome: 测量编译后策略带 0 和 32 个 Additional 时每次应用的成本。保留 Validate 对可变策略的保护。

## 证据

检查号为 14,16,40,48，状态为补证。

Compile 缓存 HSTS 且复制 Additional，Apply 与 ApplyChecked 仍运行 Validate。 [security/headers/headers.go:259](../../../security/headers/headers.go#L259)

[复审报告](../../../docs/evidence/stable-quality-review-09a7f5d3.md)

## 验收与验证

- [x] 若后续缓存校验，仍需验证直接可变 Policy 和编译后的变更语义。
- [x] 记录验证命令、状态和剩余范围。若使用既有用例满足验收，记录对应证据。

```sh
go test -race -timeout 60s ./security/headers
go test ./security/headers -run '^$' -bench '^BenchmarkCompiledHeaderValidation$' -benchmem -count=3
```

## 实施证据

[BenchmarkCompiledHeaderValidation](../../../security/headers/headers_test.go#L697) · [TestCompiledPolicyDetachesAdditionalHeaders](../../../security/headers/headers_test.go#L669)
