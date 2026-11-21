# Card 1972 — 核对异常 owner 完成后同键可以重新执行

Milestone: 稳定模块质量复审
Recipe: specs/change-recipes/add-acceptance-tests.yaml
Context Package: implementation
Priority: P2
State: done
Primary Module: middleware/singleflight
Owned Files: `middleware/singleflight/response_regression_test.go`
Depends On: 无

Goal: 等待者退出，后继成为新 owner，得到独立正常结果。
Scope: 用通道同步一次等待请求与 owner，外层在本地捕获普通 panic，再提交同键新请求。
Non-goals: 维持稳定公开签名与标准库依赖，将验证限于本卡范围。
Files: `middleware/singleflight/response_regression_test.go`。
Tests: 等待者退出，后继成为新 owner，得到独立正常结果。
Docs Sync: 记录核验结论；只在现有契约文字需要澄清时另提文档变更。
Done Definition: 完成下列验收，记录验证命令、状态及剩余限制。
Outcome: 真实 handler panic 结束首代，等待者得到 502。下一次同键请求进入新一代并返回 202。

## 证据与影响

第 9 节检查号为 24,28,34,39,47，当前证据状态为补证。 [质量规则第 9 节](../../../docs/operations/agent-code-quality-rules.md#L267)

结束时关闭等待信号并移除同键条目。一次异常结束和随后正常请求的代际切换仍需联测。 [middleware/singleflight/singleflight.go:356](../../../middleware/singleflight/singleflight.go#L356)

[复审报告](../../../docs/evidence/stable-quality-review-999d5e4d.md)

## 验收与验证

- [x] 等待者退出，后继成为新 owner，得到独立正常结果。
- [x] 记录挂载入口、具体断言和结果。已有测试能覆盖时复用并注明。

```sh
go test -race -timeout 60s ./middleware/singleflight
```

## 实施证据

- [TestHandlerPanicReleasesWaiterBeforeNextFlight](../../../middleware/singleflight/response_regression_test.go#L838)
