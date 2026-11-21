# Card 1746 — 后台排水诊断无回调 panic 边界

Milestone: 稳定模块质量复审
Recipe: specs/change-recipes/fix-bug.yaml
Context Package: implementation
Priority: P1
State: done
Primary Module: core
Owned Files: `core/lifecycle.go`, `core/lifecycle_test.go`, `docs/modules/core/README.md`
Depends On: 无

Goal: 合成 logger 首次 Info 抛 panic，后续排水可继续并按取消退出，重复关停可用。
Scope: 用局部保护包住单次诊断，保留 ticker 与关停退出路径。
Non-goals: 维持稳定公开签名与标准库依赖，将验证限于本卡范围。
Files: `core/lifecycle.go`, `core/lifecycle_test.go`, `docs/modules/core/README.md`。
Tests: 合成 logger 首次 Info 抛 panic，后续排水可继续并按取消退出，重复关停可用。
Docs Sync: 复核模块说明并验证生成数据。
Done Definition: 完成下列验收，记录验证命令、状态及剩余限制。
Outcome: 排水诊断逐条隔离 panic，首次诊断失败后仍记录下一次，取消停止输出，后续尝试可取得排水槽。

## 证据与影响

第 9 节检查号为 29,33,36,46。证据状态为源码确认。

排水循环在独立 goroutine 直接调用应用 logger；Info 的 panic 会逃逸该 goroutine。基线中的 Shutdown 错误日志保护不覆盖排水诊断。 [core/lifecycle.go:197](../../../core/lifecycle.go#L197)

[复审报告](../../../docs/evidence/stable-quality-review-d224acbc.md)

## 验收与验证

- [x] 合成 logger 首次 Info 抛 panic，后续排水可继续并按取消退出，重复关停可用。
- [x] 记录运行结果。若已有用例覆盖目标，记录用例及相关断言。

```sh
go test -race -timeout 60s ./core
make website-sync
make website-check-generated
```

## 实施证据

- [TestAcceptanceDrainContinuesAfterDiagnosticPanic](../../../core/lifecycle_test.go#L1245)

[批次验证与基准](../review-d224acbc/README.md)
