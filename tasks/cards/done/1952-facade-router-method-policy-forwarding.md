# Card 1952 — 核对根门面构造对 405 配置与默认行为的透传

Milestone: 稳定模块质量复审
Recipe: specs/change-recipes/add-acceptance-tests.yaml
Context Package: implementation
Priority: P3
State: done
Primary Module: .
Owned Files: `plumego_test.go`
Depends On: 无

Goal: 配置路径返回 405 和 Allow，默认路径保留 404，命中结果相同。
Scope: 分别用 New 和配置开启 405 的 NewWithConfig 注册同样路由，对照普通 GET 和未注册方法。
Non-goals: 维持稳定公开签名与标准库依赖，将验证限于本卡范围。
Files: `plumego_test.go`。
Tests: 配置路径返回 405 和 Allow，默认路径保留 404，命中结果相同。
Docs Sync: 记录核验结论；只在现有契约文字需要澄清时另提文档变更。
Done Definition: 完成下列验收，记录验证命令、状态及剩余限制。
Outcome: 根门面显式 405 配置经 core 传给 router，默认返回 404。命中 GET 时两个构造都按正常链执行。

## 证据与影响

第 9 节检查号为 3,5,6,37。证据状态为补证。

根门面转发配置到 core，已有地址、TLS 等行为验证。MethodNotAllowed 属 router owner，需通过门面公开服务补证。 [plumego.go:60](../../../plumego.go#L60)

[复审报告](../../../docs/evidence/stable-quality-review-2b94cf86.md)

## 验收与验证

- [x] 配置路径返回 405 和 Allow，默认路径保留 404，命中结果相同。
- [x] 记录挂载入口、具体断言和结果。已有测试能覆盖时复用并注明。

```sh
go test -race -timeout 60s .
```

## 实施证据

- [TestFacadeForwardsRouterMethodNotAllowedPolicy](../../../plumego_test.go#L222)

[批次验证与基准](../review-2b94cf86/README.md)
