# Card 1940 — 核对幂等记录快照保留 nil 与显式空响应

Milestone: 稳定模块质量复审
Recipe: specs/change-recipes/add-acceptance-tests.yaml
Context Package: implementation
Priority: P2
State: done
Primary Module: store
Owned Files: `store/idempotency/store_test.go`
Depends On: 无

Goal: 容器形状和时间字段保持，原响应不受影响。
Scope: 空、nil 和普通短体分别建立两次 Clone，并修改同时持有的副本。
Non-goals: 维持稳定公开签名与标准库依赖，将验证限于本卡范围。
Files: `store/idempotency/store_test.go`。
Tests: 容器形状和时间字段保持，原响应不受影响。
Docs Sync: 记录核验结论；只在现有契约文字需要澄清时另提文档变更。
Done Definition: 完成下列验收，记录验证命令、状态及剩余限制。
Outcome: 幂等记录复制保留 nil、显式空以及非空响应的所有权与时间字段。

## 证据与影响

第 9 节检查号为 6,16,34,41。证据状态为补证。

响应通过 slices.Clone 复制，已有普通非空样本。业务判断无缓存正文和显式空正文时需要明确形状。 [store/idempotency/store.go:93](../../../store/idempotency/store.go#L93)

[复审报告](../../../docs/evidence/stable-quality-review-2b94cf86.md)

## 验收与验证

- [x] 容器形状和时间字段保持，原响应不受影响。
- [x] 记录挂载入口、具体断言和结果。已有测试能覆盖时复用并注明。

```sh
go test -race -timeout 60s ./store/idempotency
```

## 实施证据

- [TestRecordResponseClonePreservesEmptyAndNilAndCopiesPayload](../../../store/idempotency/store_test.go#L387)

[批次验证与基准](../review-2b94cf86/README.md)
