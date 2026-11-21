# Card 1928 — 核对恢复前底层写失败是否仍保留选定终态

Milestone: 稳定模块质量复审
Recipe: specs/change-recipes/add-acceptance-tests.yaml
Context Package: implementation
Priority: P2
State: done
Primary Module: middleware
Owned Files: `middleware/recovery/recover_test.go`
Depends On: 无

Goal: 守住已提交状态，固定恢复诊断，不写第二错误体。
Scope: 底层 writer 接受一段普通响应后失败，下游再异常，比较一次明确状态和无预提交两组。
Non-goals: 维持稳定公开签名与标准库依赖，将验证限于本卡范围。
Files: `middleware/recovery/recover_test.go`。
Tests: 守住已提交状态，固定恢复诊断，不写第二错误体。
Docs Sync: 记录核验结论；只在现有契约文字需要澄清时另提文档变更。
Done Definition: 完成下列验收，记录验证命令、状态及剩余限制。
Outcome: 外层恢复在底层已经写出部分响应后只记录诊断，不重写已选状态。完全未提交的异常仍写首个错误响应。

## 证据与影响

第 9 节检查号为 22,23,25,39。证据状态为补证。

recover 以 Written 判断终态；底层返回短写错误时提交可能已发生。需要组合 panic 才能核对是否重写。 [middleware/recovery/recover.go:69](../../../middleware/recovery/recover.go#L69)

[复审报告](../../../docs/evidence/stable-quality-review-2b94cf86.md)

## 验收与验证

- [x] 守住已提交状态，固定恢复诊断，不写第二错误体。
- [x] 记录挂载入口、具体断言和结果。已有测试能覆盖时复用并注明。

```sh
go test -race -timeout 60s ./middleware/recovery
```

## 实施证据

- [TestRecoveryDoesNotRewriteAfterCommittedShortWrite](../../../middleware/recovery/recover_test.go#L704)

[批次验证与基准](../review-2b94cf86/README.md)
