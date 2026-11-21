# Card 1828 — 核对签名 key 更换后 Context 取消的发布边界

Milestone: 稳定模块质量复审
Recipe: specs/change-recipes/add-acceptance-tests.yaml
Context Package: implementation
Priority: P2
State: done
Primary Module: security
Owned Files: `security/jwt/rotation_regression_test.go`
Depends On: 无

Goal: 后续公开 KeyID 及重开状态符合提交边界，错误分类稳定。
Scope: 使用本机合成 KeyStore 和普通测试身份触发已发布及未发布两个时点。
Non-goals: 维持稳定公开签名与标准库依赖，将验证限于本卡范围。
Files: `security/jwt/rotation_regression_test.go`。
Tests: 后续公开 KeyID 及重开状态符合提交边界，错误分类稳定。
Docs Sync: 复核本卡涉及的已公开约定。
Done Definition: 完成下列验收，记录验证命令、状态及剩余限制。
Outcome: 通过受控存储区分发布前的取消与已提交后的取消。

## 证据与影响

第 9 节检查号为 26,30,31,40。证据状态为补证。

轮换含 key 记录与活动指针持久阶段，先前覆盖失败恢复。取消与发布同时发生需要明确状态。 [security/jwt/keys.go:105](../../../security/jwt/keys.go#L105)

[复审报告](../../../docs/evidence/stable-quality-review-a074eadb.md)

## 验收与验证

- [x] 后续公开 KeyID 及重开状态符合提交边界，错误分类稳定。
- [x] 记录运行结果。若已有用例覆盖目标，记录用例及相关断言。

```sh
go test -race -timeout 60s ./security/jwt
```

## 实施证据

- [TestKeyRotationCancellationFollowsPublicationBoundary](../../../security/jwt/rotation_regression_test.go#L384)

[批次验证与基准](../review-a074eadb/README.md)
