# Card 1592 — 补证 204 与 205 在缓冲 writer 中的帧信息

Milestone: 本轮质量审查
Recipe: specs/change-recipes/add-acceptance-tests.yaml
Context Package: implementation
Priority: P2
State: done
Primary Module: contract
Owned Files: `contract/response.go`, `contract/response_test.go`
Depends On: 无

Goal: 补证 204 与 205 在缓冲 writer 中的帧信息。
Scope: 补公开 WriteResponse 到受控 writer 的边界测试，记录最终净头部与中间头部。
Non-goals: 保留现有公开签名、依赖与模块边界。测试若证实新的缺陷，先记录证据再单独安排修复。
Files: `contract/response.go`, `contract/response_test.go`。
Tests: 对 204/205 对照零正文、状态、Content-Length 和 Transfer-Encoding 穿透行为。
Docs Sync: 本卡按现有契约验证。若取证表明契约需调整，先更新后续卡范围。
Done Definition: 完成下列验收和验证，记录证据与剩余风险。
Outcome: 204 和 205 在进入包装 writer 前移除无效 framing，205 继续明确零长度。

## 证据与影响

- 检查号：17,23,24,38。证据状态：补证。
- 已有真实 HTTP 无正文帧测试，204 清理由底层 writer 承担，缓冲层可看到原始 framing。 [contract/response.go:29](../../../contract/response.go#L29)
- [审查报告](../../../docs/evidence/stable-quality-review-dee7aa0f.md)

## 验收与验证

- [x] 对 204/205 对照零正文、状态、Content-Length 和 Transfer-Encoding 穿透行为。
- [x] 运行以下命令并记录结果、失败路径和未验证范围。修改 Go 文件前完成仓库预检，验证前先执行仓库格式化。

```sh
go test -race -timeout 60s ./contract
```

## 实施证据

[TestWriteResponseBodylessFramingBeforeTransport](../../../contract/response_test.go#L705) · [TestWriteResponseNoBodyFraming](../../../contract/response_test.go#L53)
