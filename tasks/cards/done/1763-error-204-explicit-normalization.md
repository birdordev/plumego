# Card 1763 — 核对不合法错误状态对 HTTP 及 JSON 的统一映射

Milestone: 稳定模块质量复审
Recipe: specs/change-recipes/add-acceptance-tests.yaml
Context Package: implementation
Priority: P2
State: done
Primary Module: contract
Owned Files: `contract/errors_test.go`
Depends On: 无

Goal: 不会以 204 或 205 发送错误外壳，保留既有默认分类。
Scope: 使用构造器和规范 writer 验证状态与机器分类。
Non-goals: 维持稳定公开签名与标准库依赖，将验证限于本卡范围。
Files: `contract/errors_test.go`。
Tests: 不会以 204 或 205 发送错误外壳，保留既有默认分类。
Docs Sync: 复核本卡涉及的已公开约定。
Done Definition: 完成下列验收，记录验证命令、状态及剩余限制。
Outcome: 以 200、204 或 205 携带错误均规范化成 500 和服务器分类。

## 证据与影响

第 9 节检查号为 6,21,22,38。证据状态为补证。

错误状态规范化、错误类型默认值及 HTTP 写入相互独立，需验证几个合法成功状态作为错误输入的输出。 [contract/errors.go:715](../../../contract/errors.go#L715)

[复审报告](../../../docs/evidence/stable-quality-review-d224acbc.md)

## 验收与验证

- [x] 不会以 204 或 205 发送错误外壳，保留既有默认分类。
- [x] 记录运行结果。若已有用例覆盖目标，记录用例及相关断言。

```sh
go test -race -timeout 60s ./contract
```

## 实施证据

- [TestWriteErrorNormalizesNonErrorStatus](../../../contract/errors_test.go#L953)

[批次验证与基准](../review-d224acbc/README.md)
