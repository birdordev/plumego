# Card 1752 — 核对 TLS 下显式禁用 HTTP/2 的实际协商

Milestone: 稳定模块质量复审
Recipe: specs/change-recipes/add-acceptance-tests.yaml
Context Package: implementation
Priority: P2
State: done
Primary Module: core
Owned Files: `core/lifecycle_test.go`
Depends On: 无

Goal: 禁用时使用 HTTP/1.1，既有连接建立和关闭语义保留。
Scope: 复用公开 Server 与本机 TLS 夹具，对照开关并检查协商版本。
Non-goals: 维持稳定公开签名与标准库依赖，将验证限于本卡范围。
Files: `core/lifecycle_test.go`。
Tests: 禁用时使用 HTTP/1.1，既有连接建立和关闭语义保留。
Docs Sync: 复核本卡涉及的已公开约定。
Done Definition: 完成下列验收，记录验证命令、状态及剩余限制。
Outcome: 真实 TLS 请求观察到开关开启协商 HTTP/2、禁用协商 HTTP/1.1，应用关停成功。

## 证据与影响

第 9 节检查号为 6,14,17,36。证据状态为补证。

Server 准备将禁用映射为空 TLSNextProto；字段断言不能替代测试客户端的协议结果。 [core/http_handler.go:163](../../../core/http_handler.go#L163)

[复审报告](../../../docs/evidence/stable-quality-review-d224acbc.md)

## 验收与验证

- [x] 禁用时使用 HTTP/1.1，既有连接建立和关闭语义保留。
- [x] 记录运行结果。若已有用例覆盖目标，记录用例及相关断言。

```sh
go test -race -timeout 60s ./core
```

## 实施证据

- [TestPreparedServerCanServeTLSViaPublicPath](../../../core/lifecycle_test.go#L675)

[批次验证与基准](../review-d224acbc/README.md)
