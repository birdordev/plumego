# Card 1836 — 核对 File 及上传配置的 nil 与空容器语义

Milestone: 稳定模块质量复审
Recipe: specs/change-recipes/add-acceptance-tests.yaml
Context Package: implementation
Priority: P2
State: done
Primary Module: store
Owned Files: `store/file/coverage_test.go`
Depends On: 无

Goal: 容器形状和可变归属符合公开约定。
Scope: 用相同人工 metadata 创建二者的快照。
Non-goals: 维持稳定公开签名与标准库依赖，将验证限于本卡范围。
Files: `store/file/coverage_test.go`。
Tests: 容器形状和可变归属符合公开约定。
Docs Sync: 复核本卡涉及的已公开约定。
Done Definition: 完成下列验收，记录验证命令、状态及剩余限制。
Outcome: File 和 PutOptions 的复制均保持带类型 nil、空切片、空 map 及嵌套序列化形状。

## 证据与影响

第 9 节检查号为 6,34,41。证据状态为补证。

File 和 PutOptions 均支持复制普通地图。需结合 typed nil 子值、空值和空容器序列化。 [store/file/types.go:99](../../../store/file/types.go#L99)

[复审报告](../../../docs/evidence/stable-quality-review-a074eadb.md)

## 验收与验证

- [x] 容器形状和可变归属符合公开约定。
- [x] 记录运行结果。若已有用例覆盖目标，记录用例及相关断言。

```sh
go test -race -timeout 60s ./store/file
```

## 实施证据

- [TestMetadataClonesPreserveSliceShapeAndOwnership](../../../store/file/coverage_test.go#L135)

[批次验证与基准](../review-a074eadb/README.md)
