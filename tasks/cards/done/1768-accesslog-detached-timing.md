# Card 1768 — 补证观察阶段的访问日志时间与字段来源

Milestone: 稳定模块质量复审
Recipe: specs/change-recipes/add-acceptance-tests.yaml
Context Package: implementation
Priority: P2
State: done
Primary Module: middleware
Owned Files: `middleware/accesslog/accesslog_test.go`
Depends On: 无

Goal: 状态、bytes、request_id 和路由字段来自约定来源。
Scope: 用普通请求和已发布 route 元数据，对照重写 URL 和缺失 route。
Non-goals: 维持稳定公开签名与标准库依赖，将验证限于本卡范围。
Files: `middleware/accesslog/accesslog_test.go`。
Tests: 状态、bytes、request_id 和路由字段来自约定来源。
Docs Sync: 复核本卡涉及的已公开约定。
Done Definition: 完成下列验收，记录验证命令、状态及剩余限制。
Outcome: 访问日志保留入站路径，另记录 writer 发布的 route。请求 ID、响应状态和返回字节在收尾时准确记录。

## 证据与影响

第 9 节检查号为 16,25,27,39。证据状态为补证。

访问日志在观察阶段读请求与 recorder；需区分入站可公开字段和响应结束才有的信息。 [middleware/accesslog/accesslog.go:23](../../../middleware/accesslog/accesslog.go#L23)

[复审报告](../../../docs/evidence/stable-quality-review-d224acbc.md)

## 验收与验证

- [x] 状态、bytes、request_id 和路由字段来自约定来源。
- [x] 记录运行结果。若已有用例覆盖目标，记录用例及相关断言。

```sh
go test -race -timeout 60s ./middleware/accesslog
```

## 实施证据

- [TestAccessLogUsesRoutePublicationAndCompletionFields](../../../middleware/accesslog/accesslog_test.go#L288)

[批次验证与基准](../review-d224acbc/README.md)
