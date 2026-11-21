# Card 1823 — 核对 Debug 旁路已编码错误的表示字段

Milestone: 稳定模块质量复审
Recipe: specs/change-recipes/add-acceptance-tests.yaml
Context Package: implementation
Priority: P2
State: done
Primary Module: middleware
Owned Files: `middleware/debug/debug_errors_test.go`
Depends On: 无

Goal: 旁路保持所有原字段，不替换表示或制造第二响应。
Scope: 向真实 wrapper 写入普通已编码人工数据，用已知摘要及类型头对照非编码响应。
Non-goals: 维持稳定公开签名与标准库依赖，将验证限于本卡范围。
Files: `middleware/debug/debug_errors_test.go`。
Tests: 旁路保持所有原字段，不替换表示或制造第二响应。
Docs Sync: 复核本卡涉及的已公开约定。
Done Definition: 完成下列验收，记录验证命令、状态及剩余限制。
Outcome: 编码响应保持原字节、编码标记和表示摘要。

## 证据与影响

第 9 节检查号为 21,23,24,39。证据状态为补证。

Debug 的重写条件包含编码、媒体类型和响应阶段。已编码响应作为独立可控路径需锁定。 [middleware/debug/debug_errors.go:340](../../../middleware/debug/debug_errors.go#L340)

[复审报告](../../../docs/evidence/stable-quality-review-a074eadb.md)

## 验收与验证

- [x] 旁路保持所有原字段，不替换表示或制造第二响应。
- [x] 记录运行结果。若已有用例覆盖目标，记录用例及相关断言。

```sh
go test -race -timeout 60s ./middleware/debug
```

## 实施证据

- [TestDebugKeepsEncodedErrorRepresentation](../../../middleware/debug/debug_errors_test.go#L99)

[批次验证与基准](../review-a074eadb/README.md)
