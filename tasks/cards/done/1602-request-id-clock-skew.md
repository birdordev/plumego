# Card 1602 — 量化时钟回拨下请求 ID 生成等待

Milestone: 本轮质量审查
Recipe: specs/change-recipes/add-acceptance-tests.yaml
Context Package: implementation
Priority: P2
State: done
Primary Module: middleware
Owned Files: `middleware/requestid/request_id_generation_test.go`
Depends On: 无

Goal: 量化时钟回拨下请求 ID 生成等待。
Scope: 使用测试时钟或合成时间测量回拨、序列耗尽及恢复。
Non-goals: 保留现有公开签名、依赖与模块边界。测试若证实新的缺陷，先记录证据再单独安排修复。
Files: `middleware/requestid/request_id_generation_test.go`。
Tests: 记录恢复时间与 ID 去重，保持编码宽度和调用入口；时钟策略变更需另行决定。
Docs Sync: 本卡按现有契约验证。若取证表明契约需调整，先更新后续卡范围。
Done Definition: 完成下列验收和验证，记录证据与剩余风险。
Outcome: 合成时间模拟墙钟落后且序号耗尽；时间追上后返回固定宽度且不重复的 ID。

## 证据与影响

- 检查号：14,16,29,35,48。证据状态：补证。
- 回拨时固定 lastMs，序列耗尽后轮询等到墙钟推进。等待边界缺少独立预算数据。 [middleware/requestid/generator.go:87](../../../middleware/requestid/generator.go#L87)
- [审查报告](../../../docs/evidence/stable-quality-review-dee7aa0f.md)

## 验收与验证

- [x] 记录恢复时间与 ID 去重，保持编码宽度和调用入口；时钟策略变更需另行决定。
- [x] 运行以下命令并记录结果、失败路径和未验证范围。修改 Go 文件前完成仓库预检，验证前先执行仓库格式化。

```sh
go test -race -timeout 60s ./middleware/requestid
```

## 实施证据

[TestGenerateRecoversWhenClockCatchesUpToExhaustedSequence](../../../middleware/requestid/request_id_generation_test.go#L221)
