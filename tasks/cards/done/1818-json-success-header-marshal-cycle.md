# Card 1818 — 核对正常业务头在普通编码错误后的保留

Milestone: 稳定模块质量复审
Recipe: specs/change-recipes/add-acceptance-tests.yaml
Context Package: implementation
Priority: P2
State: done
Primary Module: contract
Owned Files: `contract/response_test.go`
Depends On: 无

Goal: 第一次未提交也未篡改已有头，合法替代响应只提交一次。
Scope: 使用拒绝编码的无害对象，保存业务头与预置 Content-Type，随后发送合法响应。
Non-goals: 维持稳定公开签名与标准库依赖，将验证限于本卡范围。
Files: `contract/response_test.go`。
Tests: 第一次未提交也未篡改已有头，合法替代响应只提交一次。
Docs Sync: 复核本卡涉及的已公开约定。
Done Definition: 完成下列验收，记录验证命令、状态及剩余限制。
Outcome: 成功响应编码失败后，替换响应可以继续设置头部。

## 证据与影响

第 9 节检查号为 21,23,24,38。证据状态为补证。

成功外壳在提交前编码，已有错误用例主要断言未写状态。 [contract/response.go:44](../../../contract/response.go#L44)

[复审报告](../../../docs/evidence/stable-quality-review-a074eadb.md)

## 验收与验证

- [x] 第一次未提交也未篡改已有头，合法替代响应只提交一次。
- [x] 记录运行结果。若已有用例覆盖目标，记录用例及相关断言。

```sh
go test -race -timeout 60s ./contract
```

## 实施证据

- [TestSuccessEncodingFailureLeavesHeadersForReplacementResponse](../../../contract/response_test.go#L828)

[批次验证与基准](../review-a074eadb/README.md)
