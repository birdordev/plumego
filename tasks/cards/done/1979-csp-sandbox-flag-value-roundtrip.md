# Card 1979 — 核对 CSP sandbox 在空标记与值之间的切换

Milestone: 稳定模块质量复审
Recipe: specs/change-recipes/add-acceptance-tests.yaml
Context Package: implementation
Priority: P3
State: done
Primary Module: security/headers
Owned Files: `security/headers/headers_test.go`
Depends On: 无

Goal: 最终只保留一个 sandbox 指令，其余合法指令未变化。
Scope: 对合法 sandbox 选项从无值切换为有值再切回，并比较后续 BuildChecked。
Non-goals: 维持稳定公开签名与标准库依赖，将验证限于本卡范围。
Files: `security/headers/headers_test.go`。
Tests: 最终只保留一个 sandbox 指令，其余合法指令未变化。
Docs Sync: 记录核验结论；只在现有契约文字需要澄清时另提文档变更。
Done Definition: 完成下列验收，记录验证命令、状态及剩余限制。
Outcome: 合法 sandbox 从空标记切换为带值再切回只留一个指令，其他策略保留。

## 证据与影响

第 9 节检查号为 6,16,40,45，当前证据状态为补证。 [质量规则第 9 节](../../../docs/operations/agent-code-quality-rules.md#L267)

Sandbox 同时支持生成纯标记和带值指令。重复构建时两个表示之间的替换顺序需确认。 [security/headers/headers.go:588](../../../security/headers/headers.go#L588)

[复审报告](../../../docs/evidence/stable-quality-review-999d5e4d.md)

## 验收与验证

- [x] 最终只保留一个 sandbox 指令，其余合法指令未变化。
- [x] 记录挂载入口、具体断言和结果。已有测试能覆盖时复用并注明。

```sh
go test -race -timeout 60s ./security/headers
```

## 实施证据

- [TestCSPSandboxFlagAndValueReplacement](../../../security/headers/headers_test.go#L766)
