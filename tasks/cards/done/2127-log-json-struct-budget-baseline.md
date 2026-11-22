# Card 2127 — 量化 struct 字段绕过 JSON 容器预算的代价

Milestone: 稳定模块第 9 节复审
Recipe: specs/change-recipes/analysis-only.yaml
Context Package: review
Priority: P2
State: done
Primary Module: log
Owned Files: `log/json_test.go`
Depends On: 无

Goal: 量化 struct 字段绕过 JSON 容器预算的代价。
Scope: 按不同大小对照普通切片与带标签 struct，记录 bytes/op、allocs/op 和输出字节。根据数据形成限定范围的预算决策。
Non-goals: 本卡提交取证结果。后续行为变更由独立修复卡执行。
Files: `log/json_test.go`
Tests: 固定普通 struct、带 json 标签及字段自定义编码器样本，记录字段类型、精度和当前格式差异。
Docs Sync: 核对既有文档与验收结果一致，记录需要后续同步的位置。
Done Definition: 记录命令、结果、证据限制和是否需要独立修复卡。
Outcome: 本机三轮基准确认该差异符合文档。8192 个整数直接放入字段时输出约 4098 字节、约 65.6 KB/op、3089 allocs/op；经 struct 包装约 39938 字节、83.3 KB/op、20 allocs/op。带标签 struct 继续交给标准 JSON 编码，保留当前格式。工作负载上限由调用方决定。

## 证据与影响

证据状态为待补证（已测量）。第 9 节检查号为 6,16,35,43,48。同一份 1400 项数组作为字段直接记录得到 1024 项含标记，置入 struct 时输出全部 1400 项。行为符合现有文档，尚缺代表性尺寸、耗时和分配证据。 [log/json.go:209](../../../log/json.go#L209) [log/json.go:260](../../../log/json.go#L260) [log/json.go:356](../../../log/json.go#L356) [docs/modules/log/README.md:113](../../../docs/modules/log/README.md#L113)

[复审报告](../../../docs/evidence/stable-quality-review-6e8d53f9.md)

## 验收与验证

增加或复用同名基准，对固定输入分别记录输出字节、ns/op、B/op 和 allocs/op。

- [x] 固定普通 struct、带 json 标签及字段自定义编码器样本，记录字段类型、精度和当前格式差异。
- [x] 记录命令、结果、证据限制和是否需要独立修复卡。

```sh
go test -race -timeout 60s ./log
go test ./log -run '^$' -bench '^BenchmarkJSONLogStructBudget$' -benchmem -benchtime=200ms -count=3
```
