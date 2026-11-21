# Card 1754 — 核对 handler 先发布后 TLS 准备失败的重试

Milestone: 稳定模块质量复审
Recipe: specs/change-recipes/add-acceptance-tests.yaml
Context Package: implementation
Priority: P2
State: done
Primary Module: core
Owned Files: `core/core_public_test.go`
Depends On: 无

Goal: 失败期间仍可直接处理请求，合法重试只发布一个 Server。
Scope: 用临时证书文件完成先 handler、后失败、再修正的顺序。
Non-goals: 维持稳定公开签名与标准库依赖，将验证限于本卡范围。
Files: `core/core_public_test.go`。
Tests: 失败期间仍可直接处理请求，合法重试只发布一个 Server。
Docs Sync: 复核本卡涉及的已公开约定。
Done Definition: 完成下列验收，记录验证命令、状态及剩余限制。
Outcome: 直接调用入口先发布 handler，TLS 文件缺失时继续服务。证书就绪后 Prepare 成功，Server 在重试间保持同一对象。

## 证据与影响

第 9 节检查号为 28,31,36。证据状态为补证。

直接 ServeHTTP 可先冻结 handler，之后 Prepare 才校验证书。需要核对两个准备入口重叠时的阶段。 [core/http_handler.go:93](../../../core/http_handler.go#L93)

[复审报告](../../../docs/evidence/stable-quality-review-d224acbc.md)

## 验收与验证

- [x] 失败期间仍可直接处理请求，合法重试只发布一个 Server。
- [x] 记录运行结果。若已有用例覆盖目标，记录用例及相关断言。

```sh
go test -race -timeout 60s ./core
```

## 实施证据

- [TestTLSPrepareRetriesAfterDirectHandlerPublication](../../../core/core_public_test.go#L636)

[批次验证与基准](../review-d224acbc/README.md)
