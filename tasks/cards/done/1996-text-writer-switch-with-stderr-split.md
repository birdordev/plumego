# Card 1996 — 核对文本 writer 切换时的级别分流

Milestone: 稳定模块质量复审
Recipe: specs/change-recipes/add-acceptance-tests.yaml
Context Package: implementation
Priority: P2
State: done
Primary Module: log
Owned Files: `log/text_cycle_regression_test.go`
Depends On: 无

Goal: 每阶段只命中新责任 writer，保持原有分流规则。
Scope: 分两阶段替换两个普通缓冲 writer，观察 INFO 与 ERROR 去向和记录次数。
Non-goals: 维持稳定公开签名与标准库依赖，将验证限于本卡范围。
Files: `log/text_cycle_regression_test.go`。
Tests: 每阶段只命中新责任 writer，保持原有分流规则。
Docs Sync: 记录核验结论；只在现有契约文字需要澄清时另提文档变更。
Done Definition: 完成下列验收，记录验证命令、状态及剩余限制。
Outcome: 两套文本 writer 交错切换下保持 INFO 与 ERROR 独立，切换前后每条记录只写一次。

## 证据与影响

第 9 节检查号为 28,32,43,47，当前证据状态为补证。 [质量规则第 9 节](../../../docs/operations/agent-code-quality-rules.md#L267)

级别目的地由缓存决定，普通输出和错误输出可配置到不同 writer。交替改变两边后应保留配置的独立性。 [log/glog_writer.go:104](../../../log/glog_writer.go#L104)

[复审报告](../../../docs/evidence/stable-quality-review-999d5e4d.md)

## 验收与验证

- [x] 每阶段只命中新责任 writer，保持原有分流规则。
- [x] 记录挂载入口、具体断言和结果。已有测试能覆盖时复用并注明。

```sh
go test -race -timeout 60s ./log
```

## 实施证据

- [TestTextWriterChangesKeepLevelDestinationsIndependent](../../../log/text_cycle_regression_test.go#L58)
