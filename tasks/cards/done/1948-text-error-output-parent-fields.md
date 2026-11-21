# Card 1948 — 核对文本 ErrorOutput 的字段覆盖和两路输出顺序

Milestone: 稳定模块质量复审
Recipe: specs/change-recipes/add-acceptance-tests.yaml
Context Package: implementation
Priority: P3
State: done
Primary Module: log
Owned Files: `log/logger_semantics_test.go`
Depends On: 无

Goal: 正常、错误各入选定流，字段覆盖不改变父 logger，记录仍完整。
Scope: 两路 bytes.Buffer 记录并交错调用父子 logger，带普通字符串和数字字段。
Non-goals: 维持稳定公开签名与标准库依赖，将验证限于本卡范围。
Files: `log/logger_semantics_test.go`。
Tests: 正常、错误各入选定流，字段覆盖不改变父 logger，记录仍完整。
Docs Sync: 记录核验结论；只在现有契约文字需要澄清时另提文档变更。
Done Definition: 完成下列验收，记录验证命令、状态及剩余限制。
Outcome: 文本 logger 的正常与错误输出分别写入选定 sink。派生 scope 与数字覆盖不会改变父记录。

## 证据与影响

第 9 节检查号为 5,16,25,34,43。证据状态为补证。

JSON 分流与子字段已有组合证据。文本后端相同 API 但由 glog 选择输出，需分别识别各等级与覆盖字段归属。 [log/logger.go:114](../../../log/logger.go#L114)

[复审报告](../../../docs/evidence/stable-quality-review-2b94cf86.md)

## 验收与验证

- [x] 正常、错误各入选定流，字段覆盖不改变父 logger，记录仍完整。
- [x] 记录挂载入口、具体断言和结果。已有测试能覆盖时复用并注明。

```sh
go test -race -timeout 60s ./log
```

## 实施证据

- [TestTextLoggerOutputSplitPreservesParentAndChildFields](../../../log/logger_semantics_test.go#L261)

[批次验证与基准](../review-2b94cf86/README.md)
