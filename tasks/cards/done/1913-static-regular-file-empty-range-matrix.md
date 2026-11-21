# Card 1913 — 核对空静态文件在不同 HTTP 方法与条件组合中的终态

Milestone: 稳定模块质量复审
Recipe: specs/change-recipes/add-acceptance-tests.yaml
Context Package: implementation
Priority: P2
State: done
Primary Module: router
Owned Files: `router/static_test.go`
Depends On: 无

Goal: 记录 200、条件状态和范围结果，正文和首个最终状态符合标准 owner 契约。
Scope: 用临时普通空文件，比较 router.Static 与 http.ServeContent 两条本机路径。
Non-goals: 维持稳定公开签名与标准库依赖，将验证限于本卡范围。
Files: `router/static_test.go`。
Tests: 记录 200、条件状态和范围结果，正文和首个最终状态符合标准 owner 契约。
Docs Sync: 记录核验结论；只在现有契约文字需要澄清时另提文档变更。
Done Definition: 完成下列验收，记录验证命令、状态及剩余限制。
Outcome: 普通空文件的 GET、HEAD、Range 和条件请求，与 net/http ServeContent 的状态、长度和正文一致。

## 证据与影响

第 9 节检查号为 23,24,37,46。证据状态为补证。

空 seekable 文件已有 Content-Length 回归；还需联测 Range、If-Modified-Since 和 HEAD 是否沿标准 ServeContent 行为。 [router/static.go:271](../../../router/static.go#L271)

[复审报告](../../../docs/evidence/stable-quality-review-2b94cf86.md)

## 验收与验证

- [x] 记录 200、条件状态和范围结果，正文和首个最终状态符合标准 owner 契约。
- [x] 记录挂载入口、具体断言和结果。已有测试能覆盖时复用并注明。

```sh
go test -race -timeout 60s ./router
```

## 实施证据

- [TestEmptyRegularStaticFileMatchesServeContentRangesAndConditions](../../../router/static_test.go#L739)

[批次验证与基准](../review-2b94cf86/README.md)
