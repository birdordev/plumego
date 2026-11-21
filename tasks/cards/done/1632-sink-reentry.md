# Card 1632 — JSON 日志写入器重入时锁住调用方

Milestone: 稳定模块质量复审
Recipe: specs/change-recipes/analysis-only.yaml
Context Package: review
Priority: P1
State: done
Primary Module: log
Owned Files: `log/logger.go`, `log/json_test.go`, `docs/modules/log/README.md`
Depends On: 无

Goal: JSON 日志写入器重入时锁住调用方。
Scope: 先明确同步 sink 的禁止重入契约，并盘点是否已有回调需要独立诊断通道。需要库内支持时另定缓冲、并发及错误语义，避免直接去锁导致输出交错。
Non-goals: 维持公开签名和依赖；涉及现有兼容契约的调整须先核对调用边界及迁移影响。
Files: `log/logger.go`, `log/json_test.go`, `docs/modules/log/README.md`。
Tests: 用隔离进程验证同步写入约定；普通并发及共享 sink 下每条 JSON 仍完整，故障诊断通过独立路径。
Docs Sync: 复核 `docs/modules/log/README.md`，模块说明改动后核对生成文件差异。
Done Definition: 完成测试与验收记录，并标明剩余取证范围。
Outcome: 明确 Output 和 ErrorOutput 的同步写入约束。诊断走独立 sink 的回归通过，衍生 logger 仍共享原输出写锁。同 sink 同步重入继续属于不支持的配置。

## 证据

检查号为 29,32,33,43,47，状态为已复现。

Output.Write 通过同一 logger 记录写入诊断时，外层持有与衍生 logger 共享的写锁，内层再次等待该锁。两秒隔离测试超时，堆栈定位到相同日志入口。 [log/json.go:165](../../../log/json.go#L165)

[复审报告](../../../docs/evidence/stable-quality-review-09a7f5d3.md)

## 验收与验证

- [x] 用隔离进程验证同步写入约定；普通并发及共享 sink 下每条 JSON 仍完整，故障诊断通过独立路径。
- [x] 记录验证命令、状态和剩余范围。若使用既有用例满足验收，记录对应证据。

```sh
go test -race -timeout 60s ./log
make website-sync
make website-check-generated
```

## 实施证据

[TestJSONWriterDiagnosticsUseSeparateOutput](../../../log/json_test.go#L833) · [TestJSONFormatLoggerWithFieldsSharesWriterLock](../../../log/json_test.go#L714)
