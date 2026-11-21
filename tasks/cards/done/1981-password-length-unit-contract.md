# Card 1981 — 核对普通输入的密码长度计量单位

Milestone: 稳定模块质量复审
Recipe: specs/change-recipes/add-acceptance-tests.yaml
Context Package: implementation
Priority: P3
State: done
Primary Module: security/password
Owned Files: `security/password/password_test.go`
Depends On: 无

Goal: 保持当前单位和错误契约，夹具不记录输入文本。
Scope: 只用合成字符串经公共 hash 入口核对已声明边界和错误类别。
Non-goals: 维持稳定公开签名与标准库依赖，将验证限于本卡范围。
Files: `security/password/password_test.go`。
Tests: 保持当前单位和错误契约，夹具不记录输入文本。
Docs Sync: 记录核验结论；只在现有契约文字需要澄清时另提文档变更。
Done Definition: 完成下列验收，记录验证命令、状态及剩余限制。
Outcome: 用多字节合成输入确认普通 Hash 按字节计量。精确边界可用，超出时返回长度分类。

## 证据与影响

第 9 节检查号为 6,15,40,46，当前证据状态为补证。 [质量规则第 9 节](../../../docs/operations/agent-code-quality-rules.md#L267)

校验长度与强度策略位于不同入口，普通多字节输入尤其需要明确按何种单位处理。 [security/password/password.go:346](../../../security/password/password.go#L346)

[复审报告](../../../docs/evidence/stable-quality-review-999d5e4d.md)

## 验收与验证

- [x] 保持当前单位和错误契约，夹具不记录输入文本。
- [x] 记录挂载入口、具体断言和结果。已有测试能覆盖时复用并注明。

```sh
go test -race -timeout 60s ./security/password
```

## 实施证据

- [TestHashLengthCountsMultibyteInputByBytes](../../../security/password/password_test.go#L270)
