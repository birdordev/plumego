# Card 2093 — 编译策略的 HSTS 零值与正常 TLS

Milestone: 稳定模块第 9 节复审
Recipe: specs/change-recipes/add-acceptance-tests.yaml
Context Package: implementation
Priority: P2
State: done
Primary Module: security/headers
Owned Files: `security/headers/headers_test.go`, `docs/modules/security/README.md`
Depends On: 无

Goal: 编译策略的 HSTS 零值与正常 TLS
Scope: 从编译后 ApplyChecked 对照普通请求，分别访问两个空头 writer。
Non-goals: 范围限于本卡涉及的契约，保持公开签名和依赖。
Files: `security/headers/headers_test.go`, `docs/modules/security/README.md`。
Tests: 验证零配置在 TLS 和普通 HTTP 上均省略生成头，正值只在 TLS 上生成。
Docs Sync: 已同步[所属模块说明](../../../docs/modules/security/README.md)，网站生成数据核对通过。
Done Definition: 完成以下验收，记录命令、结果与剩余限制。
Outcome: 补证修正了原卡的零值假设。配置 MaxAge 为零时生成头被省略。正值在 TLS 上生成，在普通 HTTP 上省略。保持部署默认并同步说明。

## 证据与影响

证据状态为补证。第 9 节检查号为 14,20,40,46。 [质量规则第 9 节](../../../docs/operations/agent-code-quality-rules.md#L267)

原方案假设零值会生成 max-age=0。编译和应用均要求正的 MaxAge，实测零值省略生成头。 [security/headers/headers.go:399](../../../security/headers/headers.go#L399)

[复审报告](../../../docs/evidence/stable-quality-review-2f962d21.md)

## 验收与验证

- [x] 验证零配置在 TLS 和普通 HTTP 上均省略生成头，正值只在 TLS 上生成。
- [x] 已有测试覆盖相同触发和断言时可直接复用，记录具体用例。

```sh
go test -race -timeout 60s ./security/headers
```

## 实施证据

- [TestCompiledHSTSRequiresPositiveAgeAndTLS](../../../security/headers/headers_test.go#L802)
