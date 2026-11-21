# Card 1765 — 核对成功编码短写前业务头和内容类型

Milestone: 稳定模块质量复审
Recipe: specs/change-recipes/add-acceptance-tests.yaml
Context Package: implementation
Priority: P2
State: done
Primary Module: contract
Owned Files: `contract/response_test.go`
Depends On: 无

Goal: 一次提交、可分类写错误，其它约定业务头保留。
Scope: 在受控 writer 注入部分写入并捕获提交头。
Non-goals: 维持稳定公开签名与标准库依赖，将验证限于本卡范围。
Files: `contract/response_test.go`。
Tests: 一次提交、可分类写错误，其它约定业务头保留。
Docs Sync: 复核本卡涉及的已公开约定。
Done Definition: 完成下列验收，记录验证命令、状态及剩余限制。
Outcome: JSON writer 遇短写或 I/O 错误只提交一次，规范内容类型以及缓存和业务头保持。

## 证据与影响

第 9 节检查号为 21,23,24,38。证据状态为补证。

写前设置 Content-Type，短写直接返回。需要联合检查缓存头和其他业务头。 [contract/response.go:69](../../../contract/response.go#L69)

[复审报告](../../../docs/evidence/stable-quality-review-d224acbc.md)

## 验收与验证

- [x] 一次提交、可分类写错误，其它约定业务头保留。
- [x] 记录运行结果。若已有用例覆盖目标，记录用例及相关断言。

```sh
go test -race -timeout 60s ./contract
```

## 实施证据

- [TestJSONResponseReportsShortWrite](../../../contract/response_test.go#L658)

[批次验证与基准](../review-d224acbc/README.md)
