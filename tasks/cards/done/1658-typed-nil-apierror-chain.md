# Card 1658 — 核对 AsAPIError 包装链中的 typed nil

Milestone: 稳定模块质量复审
Recipe: specs/change-recipes/add-acceptance-tests.yaml
Context Package: implementation
Priority: P2
State: done
Primary Module: contract
Owned Files: `contract/errors_chain_test.go`
Depends On: 无

Goal: 核对 AsAPIError 包装链中的 typed nil。
Scope: 用人工类型链通过公开 helper 取证，不改业务错误类型。
Non-goals: 维持公开签名和依赖；涉及现有兼容契约的调整须先核对调用边界及迁移影响。
Files: `contract/errors_chain_test.go`。
Tests: 有限次遍历、无空指针访问，错误规范化策略明确。
Docs Sync: 对照原契约核验，若证据要求修订公开行为，先更新实施范围。
Done Definition: 完成测试与验收记录，并标明剩余取证范围。
Outcome: 包装链跳过 typed nil 指针，并可继续识别同一 join 中的有效分支。

## 证据

检查号为 21,22,38,46，状态为补证。

普通值、指针和包装已有覆盖，类型匹配但指针为空的入口需明确非法链的返回值。 [contract/errors.go:359](../../../contract/errors.go#L359)

[复审报告](../../../docs/evidence/stable-quality-review-09a7f5d3.md)

## 验收与验证

- [x] 有限次遍历、无空指针访问，错误规范化策略明确。
- [x] 记录验证命令、状态和剩余范围。若使用既有用例满足验收，记录对应证据。

```sh
go test -race -timeout 60s ./contract
```

## 实施证据

[TestAsAPIErrorSkipsTypedNilBranch](../../../contract/errors_chain_test.go#L215)
