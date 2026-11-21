# Card 1862 — 核对 HEAD 的 Content-Length 拒绝路径与回放

Milestone: 稳定模块质量复审
Recipe: specs/change-recipes/add-acceptance-tests.yaml
Context Package: implementation
Priority: P2
State: done
Primary Module: router
Owned Files: `router/head_regression_test.go`
Depends On: 无

Goal: 超额分类稳定，无 HEAD 正文，无第二次最终头，原声明长度仍可观察。
Scope: 固定响应声明长度，比较 HEAD 和普通 GET 的公开 writer 错误，核对头部和正文。
Non-goals: 维持稳定公开签名与标准库依赖，将验证限于本卡范围。
Files: `router/head_regression_test.go`。
Tests: 超额分类稳定，无 HEAD 正文，无第二次最终头，原声明长度仍可观察。
Docs Sync: 记录核验结论；只在现有契约文字需要澄清时另提文档变更。
Done Definition: 完成下列验收，记录验证命令、状态及剩余限制。
Outcome: 真实服务器中 GET 和 HEAD 都返回 ErrContentLength；HEAD 无正文且保留明确状态和声明长度。

## 证据与影响

第 9 节检查号为 23,24,37,46。证据状态为补证。

包装器按累计长度判断超额，正常 HEAD 会缓冲提交。需结合分次写入、ReadFrom 与过长后的零字节写入。 [router/dispatch.go:62](../../../router/dispatch.go#L62)

[复审报告](../../../docs/evidence/stable-quality-review-a8c2f28a.md)

## 验收与验证

- [x] 超额分类稳定，无 HEAD 正文，无第二次最终头，原声明长度仍可观察。
- [x] 记录挂载入口、具体断言和结果。已有测试能覆盖时复用并注明。

```sh
go test -race -timeout 60s ./router
```

## 实施证据

- [TestHeadLengthOverrunDoesNotReplaceFinalHeader](../../../router/head_regression_test.go#L513)

[批次验证与基准](../review-a8c2f28a/README.md)
