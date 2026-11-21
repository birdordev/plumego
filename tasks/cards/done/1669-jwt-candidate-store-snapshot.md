# Card 1669 — 复核候选签名密钥持久化的只读快照约定

Milestone: 稳定模块质量复审
Recipe: specs/change-recipes/add-acceptance-tests.yaml
Context Package: implementation
Priority: P2
State: done
Primary Module: security
Owned Files: `security/jwt/rotation_regression_test.go`
Depends On: 无

Goal: 复核候选签名密钥持久化的只读快照约定。
Scope: 使用合规内存适配器及受控阻塞观察阶段。
Non-goals: 维持公开签名和依赖；涉及现有兼容契约的调整须先核对调用边界及迁移影响。
Files: `security/jwt/rotation_regression_test.go`。
Tests: 提交前旧活动状态、失败分类、释放后的新状态一致且无材料输出。
Docs Sync: 对照原契约核验，若证据要求修订公开行为，先更新实施范围。
Done Definition: 完成测试与验收记录，并标明剩余取证范围。
Outcome: 复用受控 KeyStore 回归，覆盖活动指针失败、重启恢复候选以及存储阻塞时仍可验证旧活动数据。保持适配器调用契约。

## 证据

检查号为 26,30,31,34,40，状态为补证。

轮换先持久化候选后修改活动指针。现有状态测试已覆盖指针失败；仍需核对外部存储实现持有字节与重入的约束。 [security/jwt/keys.go:177](../../../security/jwt/keys.go#L177)

[复审报告](../../../docs/evidence/stable-quality-review-09a7f5d3.md)

## 验收与验证

- [x] 提交前旧活动状态、失败分类、释放后的新状态一致且无材料输出。
- [x] 记录验证命令、状态和剩余范围。若使用既有用例满足验收，记录对应证据。

```sh
go test -race -timeout 60s ./security/jwt
```

## 实施证据

[TestRotationPointerFailurePreservesActiveKey](../../../security/jwt/rotation_regression_test.go#L93) · [TestRotationCandidateSurvivesRestart](../../../security/jwt/rotation_regression_test.go#L233) · [TestVerifyContinuesWhileRotationWaitsForStore](../../../security/jwt/rotation_regression_test.go#L295)
