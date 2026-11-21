# Card 1759 — 补证静态文件 rewind 失败的单独终态

Milestone: 稳定模块质量复审
Recipe: specs/change-recipes/add-acceptance-tests.yaml
Context Package: implementation
Priority: P2
State: done
Primary Module: router
Owned Files: `router/static_test.go`
Depends On: 无

Goal: 固定 500，一次状态提交，文件关闭一次。
Scope: 用独立的合成 File 实现两个 Seek 结果并观察 Close。
Non-goals: 维持稳定公开签名与标准库依赖，将验证限于本卡范围。
Files: `router/static_test.go`。
Tests: 固定 500，一次状态提交，文件关闭一次。
Docs Sync: 复核本卡涉及的已公开约定。
Done Definition: 完成下列验收，记录验证命令、状态及剩余限制。
Outcome: SeekEnd 成功、rewind 失败后返回固定 500 且关闭文件一次，没有尝试写文件正文。

## 证据与影响

第 9 节检查号为 22,23,29,37。证据状态为补证。

探测 SeekEnd 成功而 SeekStart 失败会在响应前提前返回。需要确认不进入 ServeContent 且仅关闭一次。 [router/static.go:275](../../../router/static.go#L275)

[复审报告](../../../docs/evidence/stable-quality-review-d224acbc.md)

## 验收与验证

- [x] 固定 500，一次状态提交，文件关闭一次。
- [x] 记录运行结果。若已有用例覆盖目标，记录用例及相关断言。

```sh
go test -race -timeout 60s ./router
```

## 实施证据

- [TestStaticRewindFailureStopsBeforeServingContent](../../../router/static_test.go#L683)

[批次验证与基准](../review-d224acbc/README.md)
