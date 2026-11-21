# Card 1990 — 核对文件元数据副本中的重复容器所有权

Milestone: 稳定模块质量复审
Recipe: specs/change-recipes/add-acceptance-tests.yaml
Context Package: implementation
Priority: P2
State: done
Primary Module: store/file
Owned Files: `store/file/coverage_test.go`
Depends On: 无

Goal: 原值与快照隔离，记录同一输出内是否保留别名及适用类型。
Scope: 以重复引用作为普通文件元数据经已支持入口写入，读取两个独立结果后分别修改。
Non-goals: 维持稳定公开签名与标准库依赖，将验证限于本卡范围。
Files: `store/file/coverage_test.go`。
Tests: 原值与快照隔离，记录同一输出内是否保留别名及适用类型。
Docs Sync: 记录核验结论；只在现有契约文字需要澄清时另提文档变更。
Done Definition: 完成下列验收，记录验证命令、状态及剩余限制。
Outcome: PutOptions 和 File 的公开快照保留单份克隆内重复引用，两个输出和原始值之间互不影响。

## 证据与影响

第 9 节检查号为 31,34,41,45，当前证据状态为补证。 [质量规则第 9 节](../../../docs/operations/agent-code-quality-rules.md#L267)

文件存储公开类型承载 Metadata。不同字段引用同一普通切片时，出入所有权与别名保留是两个可观察属性。 [store/file/types.go:99](../../../store/file/types.go#L99)

[复审报告](../../../docs/evidence/stable-quality-review-999d5e4d.md)

## 验收与验证

- [x] 原值与快照隔离，记录同一输出内是否保留别名及适用类型。
- [x] 记录挂载入口、具体断言和结果。已有测试能覆盖时复用并注明。

```sh
go test -race -timeout 60s ./store/file
```

## 实施证据

- [TestMetadataClonePreservesRepeatedReferenceWithinOneSnapshot](../../../store/file/coverage_test.go#L351)
