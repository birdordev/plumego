# Card 1907 — 核对空服务并发准备时诊断只属首次发布

Milestone: 稳定模块质量复审
Recipe: specs/change-recipes/add-acceptance-tests.yaml
Context Package: implementation
Priority: P2
State: done
Primary Module: core
Owned Files: `core/core_public_test.go`
Depends On: 无

Goal: 诊断只触发一次，公开状态保持可用，全部等待结束。
Scope: 用阻塞 logger 标记首次告警起点，再从另一 goroutine 调用 Prepare 与 ServeHTTP，最后释放诊断。
Non-goals: 维持稳定公开签名与标准库依赖，将验证限于本卡范围。
Files: `core/core_public_test.go`。
Tests: 诊断只触发一次，公开状态保持可用，全部等待结束。
Docs Sync: 记录核验结论；只在现有契约文字需要澄清时另提文档变更。
Done Definition: 完成下列验收，记录验证命令、状态及剩余限制。
Outcome: 第一发布者独自记录空路由告警。告警回调阻塞时，另一个 Prepare 和 ServeHTTP 仍完成。

## 证据与影响

第 9 节检查号为 28,32,33,36,47。证据状态为补证。

无路由诊断在发布后执行。已覆盖日志 panic 后继续准备，但并发 ServeHTTP/Prepare 同时观察 handler 时，告警次数和返回状态仍需联测。 [core/http_handler.go:57](../../../core/http_handler.go#L57)

[复审报告](../../../docs/evidence/stable-quality-review-2b94cf86.md)

## 验收与验证

- [x] 诊断只触发一次，公开状态保持可用，全部等待结束。
- [x] 记录挂载入口、具体断言和结果。已有测试能覆盖时复用并注明。

```sh
go test -race -timeout 60s ./core
```

## 实施证据

- [TestEmptyAppWarningBelongsToFirstPublisher](../../../core/core_public_test.go#L775)

[批次验证与基准](../review-2b94cf86/README.md)
