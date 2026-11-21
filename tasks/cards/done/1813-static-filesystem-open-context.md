# Card 1813 — 核对静态适配器在不提供上下文的 FS 上的边界

Milestone: 稳定模块质量复审
Recipe: specs/change-recipes/analysis-only.yaml
Context Package: review
Priority: P2
State: done
Primary Module: router
Owned Files: `router/static_test.go`
Depends On: 无

Goal: 释放后至多关闭一次，边界有明确说明。
Scope: 用可控普通 FS 记录 Open 和 Close，不替换接口。
Non-goals: 维持稳定公开签名与标准库依赖，将验证限于本卡范围。
Files: `router/static_test.go`。
Tests: 释放后至多关闭一次，边界有明确说明。
Docs Sync: 复核本卡涉及的已公开约定。
Done Definition: 完成下列验收，记录验证命令、状态及剩余限制。
Outcome: 普通 http.FileSystem 的 Open 已开始时，请求取消不会中断该调用。桩释放后，处理器仍按现有行为写出文件正文并关闭文件一次。

## 证据与影响

第 9 节检查号为 16,26,29,37。证据状态为补证。

http.FileSystem 的 Open 没有 Context 参数，已有读取和关闭回归。应明确入口取消与已开始 Open 的所属责任。 [router/static.go:203](../../../router/static.go#L203)

[复审报告](../../../docs/evidence/stable-quality-review-a074eadb.md)

## 验收与验证

- [x] 释放后至多关闭一次，边界有明确说明。
- [x] 记录运行结果。若已有用例覆盖目标，记录用例及相关断言。

```sh
go test -race -timeout 60s ./router
```

## 实施证据

- [TestStaticFileSystemOwnsAnOpenThatOutlivesCancellation](../../../router/static_test.go#L712)

[批次验证与基准](../review-a074eadb/README.md)
