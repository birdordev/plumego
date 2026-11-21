# Card 1946 — 核对运行中重新启用大小轮换的计数起点

Milestone: 稳定模块质量复审
Recipe: specs/change-recipes/analysis-only.yaml
Context Package: review
Priority: P2
State: done
Primary Module: log
Owned Files: `log/glog_test.go`
Depends On: 无

Goal: 记录实际计数起点、文件状态和调用方需求，接受规则不清则先定义运维契约。
Scope: 使用受控私有文件日志夹具，禁用时写入普通记录，再启用大小阈值并检查保留和轮换时点。
Non-goals: 维持稳定公开签名与标准库依赖，将验证限于本卡范围。
Files: `log/glog_test.go`。
Tests: 记录实际计数起点、文件状态和调用方需求，接受规则不清则先定义运维契约。
Docs Sync: 记录核验结论；只在现有契约文字需要澄清时另提文档变更。
Done Definition: 完成下列验收，记录验证命令、状态及剩余限制。
Outcome: 内部文件后端禁用轮换时写入约 1 MiB 未累计。重新启用并写入少量数据后从旧计数继续。若要按总文件大小轮换，应在启用边界用同一锁序刷新 writer 大小。

## 证据与影响

第 9 节检查号为 14,28,29,43。证据状态为补证。

内部文件后端禁用 MaxSize 时跳过字节累计。重新启用后初始值如何和当前文件大小对齐，尚无独立验证。 [log/glog_rotation.go:95](../../../log/glog_rotation.go#L95)

[复审报告](../../../docs/evidence/stable-quality-review-2b94cf86.md)

## 验收与验证

- [x] 记录实际计数起点、文件状态和调用方需求，接受规则不清则先定义运维契约。
- [x] 记录挂载入口、具体断言和结果。已有测试能覆盖时复用并注明。

## 实施证据

Go overlay 仅向本机 log 测试追加夹具。禁用时已有 1048628 字节，重新启用后只追踪随后 64 字节。需要按物理文件容量启用轮换时，先确定内部调用方契约，再于启用边界在 writer 归属内刷新计数。

```sh
go test -overlay=/tmp/2b94-log-overlay.json -race -timeout 40s -v ./log -run '^TestPrivateRotationEnablesFromTrackedBytes$'
```

[批次验证与基准](../review-2b94cf86/README.md)
