# Card 1970 — 核对合法重复 CORS 头名称的输出

Milestone: 稳定模块质量复审
Recipe: specs/change-recipes/add-acceptance-tests.yaml
Context Package: implementation
Priority: P2
State: done
Primary Module: middleware/cors
Owned Files: `middleware/cors/cors_test.go`
Depends On: 无

Goal: 预检头集合及是否访问下游符合配置，并记录现有输出顺序。
Scope: 使用本地允许来源和两个普通自定义头，改变重复值和大小写。对未经整理的两个合法名称保留列表原始顺序作为对照。
Non-goals: 维持稳定公开签名与标准库依赖，将验证限于本卡范围。
Files: `middleware/cors/cors_test.go`。
Tests: 预检头集合及是否访问下游符合配置，并记录现有输出顺序。
Docs Sync: 记录核验结论；只在现有契约文字需要澄清时另提文档变更。
Done Definition: 完成下列验收，记录验证命令、状态及剩余限制。
Outcome: 预检响应保留合法重复请求头名称的大小写和顺序，成功分支不调用下游。

## 证据与影响

第 9 节检查号为 13,16,39,46，当前证据状态为补证。 [质量规则第 9 节](../../../docs/operations/agent-code-quality-rules.md#L267)

预检头列表按名称解析。合法的重复名称或大小写差异可能影响客户端看到的列表。 [middleware/cors/cors.go:242](../../../middleware/cors/cors.go#L242)

[复审报告](../../../docs/evidence/stable-quality-review-999d5e4d.md)

## 验收与验证

- [x] 预检头集合及是否访问下游符合配置，并记录现有输出顺序。
- [x] 记录挂载入口、具体断言和结果。已有测试能覆盖时复用并注明。

```sh
go test -race -timeout 60s ./middleware/cors
```

## 实施证据

- [TestPreflightKeepsRequestedHeaderCaseAndOrder](../../../middleware/cors/cors_test.go#L720)
