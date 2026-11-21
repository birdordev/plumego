# Card 1874 — 核对访问日志在底层短写后选择已匹配路由

Milestone: 稳定模块质量复审
Recipe: specs/change-recipes/add-acceptance-tests.yaml
Context Package: implementation
Priority: P2
State: done
Primary Module: middleware
Owned Files: `middleware/accesslog/accesslog_test.go`
Depends On: 无

Goal: 访问日志使用选定的路由模板、状态和确实写入的字节，错误不增加第二次记录。
Scope: 注入只接收部分普通正文的 writer，通过真实 router 处理，捕获一条访问日志。
Non-goals: 维持稳定公开签名与标准库依赖，将验证限于本卡范围。
Files: `middleware/accesslog/accesslog_test.go`。
Tests: 访问日志使用选定的路由模板、状态和确实写入的字节，错误不增加第二次记录。
Docs Sync: 记录核验结论；只在现有契约文字需要澄清时另提文档变更。
Done Definition: 完成下列验收，记录验证命令、状态及剩余限制。
Outcome: 真实路由经底层短写时访问日志记录匹配模板、名称、最终状态和已接受的字节。

## 证据与影响

第 9 节检查号为 23,25,27,33,39。证据状态为补证。

访问日志和 tracing 都经公共遥测助手，但组合读取路由模板与实际写入字节需单独验证。 [middleware/accesslog/accesslog.go:23](../../../middleware/accesslog/accesslog.go#L23)

[复审报告](../../../docs/evidence/stable-quality-review-a8c2f28a.md)

## 验收与验证

- [x] 访问日志使用选定的路由模板、状态和确实写入的字节，错误不增加第二次记录。
- [x] 记录挂载入口、具体断言和结果。已有测试能覆盖时复用并注明。

```sh
go test -race -timeout 60s ./middleware/accesslog
```

## 实施证据

- [TestAccessLogRecordsMatchedRouteAfterShortWrite](../../../middleware/accesslog/accesslog_test.go#L336)

[批次验证与基准](../review-a8c2f28a/README.md)
