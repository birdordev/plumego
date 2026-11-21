# Card 1918 — 核对多字节 Request ID 裁剪后的接受长度与上下文继承

Milestone: 稳定模块质量复审
Recipe: specs/change-recipes/add-acceptance-tests.yaml
Context Package: implementation
Priority: P2
State: done
Primary Module: contract
Owned Files: `contract/request_id_test.go`
Depends On: 无

Goal: 合法 ID 进入响应标识，超界值保留已有 Context 标识。
Scope: 使用普通 Unicode 标签的 127、128 和 129 字节值，分别带边界空格。
Non-goals: 维持稳定公开签名与标准库依赖，将验证限于本卡范围。
Files: `contract/request_id_test.go`。
Tests: 合法 ID 进入响应标识，超界值保留已有 Context 标识。
Docs Sync: 记录核验结论；只在现有契约文字需要澄清时另提文档变更。
Done Definition: 完成下列验收，记录验证命令、状态及剩余限制。
Outcome: Request ID 切除边界空白后再按 UTF-8 字节限制，129 字节输入保留先前 Context ID。

## 证据与影响

第 9 节检查号为 6,18,25,27,38。证据状态为补证。

合同按裁剪后 128 字节限制而非字符数。多字节边界与保留之前 ID 需要在同一请求链验证。 [contract/request_id.go:45](../../../contract/request_id.go#L45)

[复审报告](../../../docs/evidence/stable-quality-review-2b94cf86.md)

## 验收与验证

- [x] 合法 ID 进入响应标识，超界值保留已有 Context 标识。
- [x] 记录挂载入口、具体断言和结果。已有测试能覆盖时复用并注明。

```sh
go test -race -timeout 60s ./contract
```

## 实施证据

- [TestRequestIDByteLimitTrimsBeforeChoosingInheritedValue](../../../contract/request_id_test.go#L120)

[批次验证与基准](../review-2b94cf86/README.md)
