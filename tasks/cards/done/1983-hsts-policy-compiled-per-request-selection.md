# Card 1983 — 核对编译后的 HSTS 策略在普通 HTTP 与 TLS 间独立

Milestone: 稳定模块质量复审
Recipe: specs/change-recipes/add-acceptance-tests.yaml
Context Package: implementation
Priority: P3
State: done
Primary Module: security/headers
Owned Files: `security/headers/headers_test.go`
Depends On: 无

Goal: 两种请求只得到各自应有的 HSTS 结果，策略复用无需改变配置。
Scope: 用相同编译策略依次处理两个独立 writer 的普通本机 HTTP 与 HTTPS 夹具。
Non-goals: 维持稳定公开签名与标准库依赖，将验证限于本卡范围。
Files: `security/headers/headers_test.go`。
Tests: 两种请求只得到各自应有的 HSTS 结果，策略复用无需改变配置。
Docs Sync: 记录核验结论；只在现有契约文字需要澄清时另提文档变更。
Done Definition: 完成下列验收，记录验证命令、状态及剩余限制。
Outcome: 重复使用编译后的策略时，本地 HTTPS 请求获得 HSTS，普通 HTTP 请求保持空，之后的 HTTPS 结果一致。

## 证据与影响

第 9 节检查号为 6,16,31,40，当前证据状态为补证。 [质量规则第 9 节](../../../docs/operations/agent-code-quality-rules.md#L267)

Compile 预生成配置内容，Apply 每次判断请求是否 HTTPS。共享策略复用时应确认没有跨请求残留。 [security/headers/headers.go:184](../../../security/headers/headers.go#L184)

[复审报告](../../../docs/evidence/stable-quality-review-999d5e4d.md)

## 验收与验证

- [x] 两种请求只得到各自应有的 HSTS 结果，策略复用无需改变配置。
- [x] 记录挂载入口、具体断言和结果。已有测试能覆盖时复用并注明。

```sh
go test -race -timeout 60s ./security/headers
```

## 实施证据

- [TestCompiledPolicyKeepsHSTSRequestLocal](../../../security/headers/headers_test.go#L779)
