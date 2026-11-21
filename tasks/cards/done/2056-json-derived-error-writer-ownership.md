# Card 2056 — 派生 JSON logger 保留错误 writer 分流

Milestone: 稳定模块第 9 节复审
Recipe: specs/change-recipes/add-acceptance-tests.yaml
Context Package: implementation
Priority: P2
State: done
Primary Module: log
Owned Files: `log/json_test.go`
Depends On: 无

Goal: 派生 JSON logger 保留错误 writer 分流
Scope: 从父实例派生两层字段，用不同 writer 接收普通和错误记录。
Non-goals: 范围限于本卡涉及的契约，保持公开签名和依赖。
Files: `log/json_test.go`。
Tests: 分级一致、每条 JSON 独立可解码、子字段不会回写父实例。
Docs Sync: 核对新增验收与已有模块说明一致，记录证据和限制。
Done Definition: 完成以下验收，记录命令、结果与剩余限制。
Outcome: JSON 子级和孙级分别写普通与错误输出，每行独立解码，父级字段保持独立。

## 证据与影响

证据状态为补证。第 9 节检查号为 5,32,34,43。 [质量规则第 9 节](../../../docs/operations/agent-code-quality-rules.md#L267)

JSON 派生 logger 共享底层写入锁并按级别选 writer。 [log/json.go:396](../../../log/json.go#L396)

[复审报告](../../../docs/evidence/stable-quality-review-c3a77410.md)

## 验收与验证

- [x] 分级一致、每条 JSON 独立可解码、子字段不会回写父实例。
- [x] 已有测试覆盖相同触发和断言时可直接复用，记录具体用例。

```sh
go test -race -timeout 60s ./log
```

## 实施证据

- [TestJSONDerivedScopeStaysWithSelectedOutput](../../../log/json_test.go#L973)
