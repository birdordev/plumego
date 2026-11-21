# Card 1956 — 重新启用文件轮换时漏算关闭期间写入的内容

Milestone: 稳定模块质量复审
Recipe: specs/change-recipes/fix-bug.yaml
Context Package: implementation
Priority: P2
State: done
Primary Module: log
Owned Files: `log/glog_rotation.go`, `log/glog_test.go`
Depends On: 无

Goal: 关闭期间文件和计数一致，启用后首条写入按阈值触发轮换。覆盖多个级别、重复切换和无文件实例。
Scope: 在现有串行写入边界继续计入已写文件字节，只暂停阈值判断和归档。避免重复计入已有文件。
Non-goals: 维持稳定公开签名与标准库依赖，将验证限于本卡范围。
Files: `log/glog_rotation.go`, `log/glog_test.go`。
Tests: 关闭期间文件和计数一致，启用后首条写入按阈值触发轮换。覆盖多个级别、重复切换和无文件实例。
Docs Sync: 记录核验结论；只在现有契约文字需要澄清时另提文档变更。
Done Definition: 完成下列验收，记录验证命令、状态及剩余限制。
Outcome: 轮换禁用期间仍按实际写入计数。两轮开关均在启用后的首笔写入轮换多个级别，无文件路径不创建计数。

## 证据与影响

第 9 节检查号为 32,34,43,46，当前证据状态为已复现。 [质量规则第 9 节](../../../docs/operations/agent-code-quality-rules.md#L267)

内部文件后端禁用轮换期间写到 1048628 字节，按 1 MiB 启用后再写一条，跟踪大小仍为 64 字节。阈值判断忽略禁用时产生的内容。 [log/glog_rotation.go:96](../../../log/glog_rotation.go#L96)

[复审报告](../../../docs/evidence/stable-quality-review-999d5e4d.md)

## 验收与验证

- [x] 关闭期间文件和计数一致，启用后首条写入按阈值触发轮换。覆盖多个级别、重复切换和无文件实例。
- [x] 记录挂载入口、具体断言和结果。已有测试能覆盖时复用并注明。

```sh
go test -race -timeout 60s ./log
```

## 实施证据

- [TestFileRotationTracksBytesWhileDisabled](../../../log/glog_test.go#L1787)
- [TestRotationCountsFileBytesWhenMirrorFails](../../../log/glog_test.go#L1057)

所属回归在 999d5e4d 的生产代码 overlay 上如预期失败，修改后连续十次竞态运行通过。
