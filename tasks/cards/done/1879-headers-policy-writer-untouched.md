# Card 1879 — 核对 Policy 编译副本与失败前的 writer 归属

Milestone: 稳定模块质量复审
Recipe: specs/change-recipes/add-acceptance-tests.yaml
Context Package: implementation
Priority: P2
State: done
Primary Module: security
Owned Files: `security/headers/headers_test.go`
Depends On: 无

Goal: 配置失败不修改已存在头，成功时预期字段按配置生成。
Scope: 以无害合成配置和普通请求比较原 policy、编译副本及失败 writer。
Non-goals: 维持稳定公开签名与标准库依赖，将验证限于本卡范围。
Files: `security/headers/headers_test.go`。
Tests: 配置失败不修改已存在头，成功时预期字段按配置生成。
Docs Sync: 记录核验结论；只在现有契约文字需要澄清时另提文档变更。
Done Definition: 完成下列验收，记录验证命令、状态及剩余限制。
Outcome: 编译安全头策略后与源 map 脱开，原策略校验失败时保留 writer 预置头部。

## 证据与影响

第 9 节检查号为 14,20,34,40。证据状态为补证。

ApplyChecked 先验证再写头，Compile 沿不同入口持有副本。需观察预置响应头在合法替换和配置错误时的变化。 [security/headers/headers.go:259](../../../security/headers/headers.go#L259)

[复审报告](../../../docs/evidence/stable-quality-review-a8c2f28a.md)

## 验收与验证

- [x] 配置失败不修改已存在头，成功时预期字段按配置生成。
- [x] 记录挂载入口、具体断言和结果。已有测试能覆盖时复用并注明。

```sh
go test -race -timeout 60s ./security/headers
```

## 实施证据

- [TestCompiledPolicyKeepsPrepopulatedWriterOnValidationFailure](../../../security/headers/headers_test.go#L732)

[批次验证与基准](../review-a8c2f28a/README.md)
