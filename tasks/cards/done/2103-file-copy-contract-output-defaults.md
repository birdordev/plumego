# Card 2103 — 文件元数据类型的可选零值与所有权

Milestone: 稳定模块第 9 节复审
Recipe: specs/change-recipes/add-acceptance-tests.yaml
Context Package: implementation
Priority: P3
State: done
Primary Module: store/file
Owned Files: `store/file/coverage_test.go`
Depends On: 无

Goal: 文件元数据类型的可选零值与所有权
Scope: 拷贝后从原 map 和输出 map 分别写值，再检查 PutOptions 的零初始化。
Non-goals: 范围限于本卡涉及的契约，保持公开签名和依赖。
Files: `store/file/coverage_test.go`。
Tests: 原与副本隔离，nil 和显式空 map 保持已声明区别。
Docs Sync: 核对新增验收与已有模块说明一致，记录证据和限制。
Done Definition: 完成以下验收，记录命令、结果与剩余限制。
Outcome: 补证零值和显式空元数据的形状。上传选项与文件的普通嵌套切片在输入输出两端独立。

## 证据与影响

证据状态为补证。第 9 节检查号为 16,34,41,45。 [质量规则第 9 节](../../../docs/operations/agent-code-quality-rules.md#L267)

PutOptions、File 和 FileStat 定义稳定数据形状。核对空 map 以及受支持的普通嵌套切片。 [store/file/types.go:57](../../../store/file/types.go#L57)

[复审报告](../../../docs/evidence/stable-quality-review-2f962d21.md)

## 验收与验证

- [x] 原与副本隔离，nil 和显式空 map 保持已声明区别。
- [x] 已有测试覆盖相同触发和断言时可直接复用，记录具体用例。

```sh
go test -race -timeout 60s ./store/file
```

## 实施证据

- [TestUploadAndFileMetadataCopiesFromSameSourceDetach](../../../store/file/coverage_test.go#L297)
- [TestMetadataClonesPreserveSliceShapeAndOwnership](../../../store/file/coverage_test.go#L135)
