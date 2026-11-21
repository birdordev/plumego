# Card 1784 — 核对 PutOptions 与 File.Clone 的 metadata 生命周期

Milestone: 稳定模块质量复审
Recipe: specs/change-recipes/add-acceptance-tests.yaml
Context Package: implementation
Priority: P2
State: done
Primary Module: store
Owned Files: `store/file/coverage_test.go`
Depends On: 无

Goal: 支持的容器互不污染，未承诺的对象保持原约定。
Scope: 从同一普通 metadata 建立两个上传配置快照和一个 File 快照。
Non-goals: 维持稳定公开签名与标准库依赖，将验证限于本卡范围。
Files: `store/file/coverage_test.go`。
Tests: 支持的容器互不污染，未承诺的对象保持原约定。
Docs Sync: 复核本卡涉及的已公开约定。
Done Definition: 完成下列验收，记录验证命令、状态及剩余限制。
Outcome: 同一上传 metadata 的多个快照和 File 副本互不污染，维持支持容器的所有权边界。

## 证据与影响

第 9 节检查号为 16,34,41。证据状态为补证。

上传配置和返回对象用相同复制策略。已有各自单测，尚需组合比较分离的所有权。 [store/file/types.go:57](../../../store/file/types.go#L57)

[复审报告](../../../docs/evidence/stable-quality-review-d224acbc.md)

## 验收与验证

- [x] 支持的容器互不污染，未承诺的对象保持原约定。
- [x] 记录运行结果。若已有用例覆盖目标，记录用例及相关断言。

```sh
go test -race -timeout 60s ./store/file
```

## 实施证据

- [TestUploadAndFileMetadataCopiesFromSameSourceDetach](../../../store/file/coverage_test.go#L296)

[批次验证与基准](../review-d224acbc/README.md)
