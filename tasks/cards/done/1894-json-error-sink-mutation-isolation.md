# Card 1894 — 核对 ErrorOutput 分流与衍生字段组合

Milestone: 稳定模块质量复审
Recipe: specs/change-recipes/add-acceptance-tests.yaml
Context Package: implementation
Priority: P3
State: done
Primary Module: log
Owned Files: `log/json_test.go`
Depends On: 无

Goal: 只在对应流出现，子覆盖不修改父记录，输出均为合法单行 JSON。
Scope: 两个普通 buffer 同时收集 Info、Warn 和 Error，每次附不同字段副本。
Non-goals: 维持稳定公开签名与标准库依赖，将验证限于本卡范围。
Files: `log/json_test.go`。
Tests: 只在对应流出现，子覆盖不修改父记录，输出均为合法单行 JSON。
Docs Sync: 记录核验结论；只在现有契约文字需要澄清时另提文档变更。
Done Definition: 完成下列验收，记录验证命令、状态及剩余限制。
Outcome: JSON 按既有规则分流到 Output 与 ErrorOutput，衍生字段保持每条日志各自范围。

## 证据与影响

第 9 节检查号为 5,16,25,34,43。证据状态为补证。

文件后端之外，JSON 按 ErrorOutput 分流，衍生 logger 共享写锁。需对照两个独立输出及父子字段。 [log/json.go:396](../../../log/json.go#L396)

[复审报告](../../../docs/evidence/stable-quality-review-a8c2f28a.md)

## 验收与验证

- [x] 只在对应流出现，子覆盖不修改父记录，输出均为合法单行 JSON。
- [x] 记录挂载入口、具体断言和结果。已有测试能覆盖时复用并注明。

```sh
go test -race -timeout 60s ./log
```

## 实施证据

- [TestJSONDerivedScopeStaysWithSelectedOutput](../../../log/json_test.go#L973)

[批次验证与基准](../review-a8c2f28a/README.md)
