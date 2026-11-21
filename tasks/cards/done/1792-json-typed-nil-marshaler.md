# Card 1792 — 核对结构化字段的 typed nil 自定义编码

Milestone: 稳定模块质量复审
Recipe: specs/change-recipes/add-acceptance-tests.yaml
Context Package: implementation
Priority: P2
State: done
Primary Module: log
Owned Files: `log/json_test.go`
Depends On: 无

Goal: 输出可解析，安全兄弟字段保留，输出一次。
Scope: 用安全 nil receiver 与人工占位值对照两种格式。
Non-goals: 维持稳定公开签名与标准库依赖，将验证限于本卡范围。
Files: `log/json_test.go`。
Tests: 输出可解析，安全兄弟字段保留，输出一次。
Docs Sync: 复核本卡涉及的已公开约定。
Done Definition: 完成下列验收，记录验证命令、状态及剩余限制。
Outcome: typed nil 自定义字段编码为 null，兄弟字段保留，结构化输出只形成一条记录。

## 证据与影响

第 9 节检查号为 21,33,34,43。证据状态为补证。

自定义编码的 panic 与普通坏值已有测试，typed nil 指针可能通过特殊 nil receiver 表示多种结果。 [log/json.go:282](../../../log/json.go#L282)

[复审报告](../../../docs/evidence/stable-quality-review-d224acbc.md)

## 验收与验证

- [x] 输出可解析，安全兄弟字段保留，输出一次。
- [x] 记录运行结果。若已有用例覆盖目标，记录用例及相关断言。

```sh
go test -race -timeout 60s ./log
```

## 实施证据

- [TestTypedNilMarshalerKeepsValidSiblingFields](../../../log/json_test.go#L906)

[批次验证与基准](../review-d224acbc/README.md)
