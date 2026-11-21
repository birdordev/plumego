# Card 1963 — 比对两种静态文件入口的 If-Range 行为

Milestone: 稳定模块质量复审
Recipe: specs/change-recipes/add-acceptance-tests.yaml
Context Package: implementation
Priority: P2
State: done
Primary Module: router
Owned Files: `router/static_test.go`
Depends On: 无

Goal: 匹配返回相同 206 范围与内容，不匹配返回完整实体。
Scope: 用固定修改时间和普通短文件，对比 Static 与 StaticFS 的日期验证条件。
Non-goals: 维持稳定公开签名与标准库依赖，将验证限于本卡范围。
Files: `router/static_test.go`。
Tests: 匹配返回相同 206 范围与内容，不匹配返回完整实体。
Docs Sync: 记录核验结论；只在现有契约文字需要澄清时另提文档变更。
Done Definition: 完成下列验收，记录验证命令、状态及剩余限制。
Outcome: Static 和 StaticFS 在固定 If-Range 日期下均返回同一 206 和范围内容，日期不匹配时返回完整实体。

## 证据与影响

第 9 节检查号为 6,12,17,37，当前证据状态为补证。 [质量规则第 9 节](../../../docs/operations/agent-code-quality-rules.md#L267)

两个文件入口最终调用同一内容写入路径。范围请求的日期匹配与不匹配对照尚需独立观察。 [router/static.go:271](../../../router/static.go#L271)

[复审报告](../../../docs/evidence/stable-quality-review-999d5e4d.md)

## 验收与验证

- [x] 匹配返回相同 206 范围与内容，不匹配返回完整实体。
- [x] 记录挂载入口、具体断言和结果。已有测试能覆盖时复用并注明。

```sh
go test -race -timeout 60s ./router
```

## 实施证据

- [TestStaticIfRangeDateParity](../../../router/static_test.go#L773)
