# Card 1914 — 核对 HEAD Reader 中途失败时已消费字节和头快照

Milestone: 稳定模块质量复审
Recipe: specs/change-recipes/add-acceptance-tests.yaml
Context Package: implementation
Priority: P2
State: done
Primary Module: router
Owned Files: `router/head_regression_test.go`
Depends On: 无

Goal: 保留底层读错误，HEAD 头部及已观察长度不随错误回退。
Scope: 使用只给一段普通数据后报错的 Reader，记录回调收到的字节数、错误、最终头和无正文。
Non-goals: 维持稳定公开签名与标准库依赖，将验证限于本卡范围。
Files: `router/head_regression_test.go`。
Tests: 保留底层读错误，HEAD 头部及已观察长度不随错误回退。
Docs Sync: 记录核验结论；只在现有契约文字需要澄清时另提文档变更。
Done Definition: 完成下列验收，记录验证命令、状态及剩余限制。
Outcome: HEAD 的 Reader 中途失败保留已消费字节及原始错误、内容类型和推断长度，底层没有正文。

## 证据与影响

第 9 节检查号为 23,24,37。证据状态为补证。

ReadFrom 复用 Write 做长度和嗅探；已有普通 drain 和超额回归。中途读错误与首次字节嗅探组合待核对。 [router/dispatch.go:72](../../../router/dispatch.go#L72)

[复审报告](../../../docs/evidence/stable-quality-review-2b94cf86.md)

## 验收与验证

- [x] 保留底层读错误，HEAD 头部及已观察长度不随错误回退。
- [x] 记录挂载入口、具体断言和结果。已有测试能覆盖时复用并注明。

```sh
go test -race -timeout 60s ./router
```

## 实施证据

- [TestHeadReadFromFailureKeepsConsumedLengthAndContentType](../../../router/head_regression_test.go#L552)

[批次验证与基准](../review-2b94cf86/README.md)
