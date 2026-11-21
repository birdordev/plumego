# 稳定模块质量复审实施记录

以 a8c2f28a 为基线，已执行并归档 **52 张卡**。内置 Tracker 使用批次实际结果，历史导出验证整数分页和格式，gzip 按响应提交状态收尾。历史留存、Reset 和注册交叠三张卡按分析方案出具取证。 [基线报告](../../../docs/evidence/stable-quality-review-a8c2f28a.md)

表中的优先级与证据状态保留审查时的分类。逐卡结论与测试位于归档。

| 卡片 | 优先级 | 证据状态 | 主模块 | 第 9 节检查号 |
|---|---|---|---|---|
| [1848 聚合未就绪时 Tracker 把未完成组件累计为成功](../done/1848-tracker-count-aborted-outcomes.md) | P1 | 已复现 | x/observability/ops | 28,31,32,42,46 |
| [1849 压缩包装在 Goexit 路径隐式提交 200](../done/1849-gzip-abnormal-header-commit.md) | P2 | 已复现 | middleware | 20,23,24,39,47 |
| [1850 超出历史尾部的大 offset 错报还有下一页](../done/1850-history-offset-overflow.md) | P2 | 已复现 | x/observability/ops | 14,18,38,42 |
| [1851 历史导出的无效分页参数悄悄回退到默认值](../done/1851-history-invalid-pagination.md) | P2 | 已复现 | x/observability/ops | 14,18,21,22,42 |
| [1852 Tracker 平均耗时计算会把非负样本累积成负数](../done/1852-tracker-duration-average-overflow.md) | P2 | 已复现 | x/observability/ops | 31,35,42,44,46 |
| [1853 每次健康聚合都追加历史且没有保留上限](../done/1853-tracker-history-retention.md) | P2 | 源码确认 | x/observability/ops | 14,29,35,42,48 |
| [1854 不支持的导出格式仍查询整个历史](../done/1854-history-format-check-before-read.md) | P2 | 源码确认 | x/observability/ops | 10,18,35,42,48 |
| [1898 Tracker 的可序列化统计保留原始依赖错误文本](../done/1898-tracker-metrics-diagnostic-boundary.md) | P2 | 源码确认 | x/observability/ops | 22,25,38,42 |
| [1855 验证并发 Shutdown 的诊断所有权移交边界](../done/1855-drain-owner-handoff.md) | P2 | 补证 | core | 26,28,29,32,36 |
| [1856 联测连接接管后的排空计数及资源归属](../done/1856-drain-hijack-ownership.md) | P2 | 补证 | core | 24,28,29,36 |
| [1857 核对无效 TLS 配置时直接 HTTP 与显式准备的状态隔离](../done/1857-tls-config-invalid-then-direct-publish.md) | P2 | 补证 | core | 6,15,28,31,36 |
| [1858 核对 Ready 回调期间 Stop、Add 和再次 Ready 的冲突处理](../done/1858-lifecycle-ready-stop-add-overlap.md) | P2 | 补证 | core | 28,29,33,36,47 |
| [1860 验证命名通配符反向路由对内部空段的往返](../done/1860-reverse-wildcard-empty-inner-segment.md) | P2 | 补证 | router | 6,18,37,46 |
| [1861 核对 RouteOption 执行期间 Freeze 的发表边界](../done/1861-registration-option-freeze-metadata.md) | P2 | 补证 | router | 19,28,31,33,37 |
| [1862 核对 HEAD 的 Content-Length 拒绝路径与回放](../done/1862-head-length-overrun-after-final.md) | P2 | 补证 | router | 23,24,37,46 |
| [1863 核对 Freeze 前确定的 405 开关及冷热等价](../done/1863-allow-toggle-after-freeze.md) | P2 | 补证 | router | 6,28,32,37 |
| [1865 核对协议无正文状态与错误响应形状](../done/1865-error-204-205-output-matrix.md) | P2 | 补证 | contract | 6,21,23,24,38 |
| [1866 核对共享底层数组的有类型切片在 Error 详情中的视图](../done/1866-typed-detail-slice-distinct-views.md) | P2 | 补证 | contract | 6,33,34,35,38 |
| [1867 核对采样标志与部分有效追踪字段的入口策略](../done/1867-trace-sampled-invalid-span.md) | P2 | 补证 | contract | 6,18,25,27,38 |
| [1868 核对 ErrorBuilder 设定的 RequestID 优先级及降级](../done/1868-error-builder-overrides-request-id.md) | P2 | 补证 | contract | 6,21,25,27,38 |
| [1869 核对父取消后迟到 panic 的遥测与终态](../done/1869-timeout-cancel-late-panic-ownership.md) | P2 | 补证 | middleware | 23,26,29,33,39 |
| [1870 补证压缩包装空正文时的 Trailer 交付顺序](../done/1870-compression-header-only-trailer-order.md) | P2 | 补证 | middleware | 23,24,39 |
| [1871 核对等待者超时与独立取消的归属](../done/1871-singleflight-timing-parent-context.md) | P2 | 补证 | middleware | 26,28,29,39,47 |
| [1872 核对无效自定义生成器下的禁写请求头配置](../done/1872-requestid-header-disabled-invalid-generator.md) | P2 | 补证 | middleware | 14,20,25,27,39 |
| [1873 核对信息响应及外层观察器只记录一次最终 500](../done/1873-recovery-early-hints-then-panic.md) | P2 | 补证 | middleware | 20,23,24,25,39 |
| [1874 核对访问日志在底层短写后选择已匹配路由](../done/1874-accesslog-fanout-writer-error.md) | P2 | 补证 | middleware | 23,25,27,33,39 |
| [1875 核对并发限制在下游异常退出时交还占位](../done/1875-concurrencylimit-queue-drain-abnormal.md) | P2 | 补证 | middleware | 20,26,28,29,39 |
| [1876 核对内存密钥容器替换过期条目时的所有权](../done/1876-keystore-expiry-source-updated.md) | P2 | 补证 | security | 26,28,34,40 |
| [1877 补证 Principal 快照的 nil 与空集合形状](../done/1877-authn-context-empty-slice-shapes.md) | P2 | 补证 | security | 6,27,34,40 |
| [1878 核对口令强度字符计数和 UTF-8 可见长度](../done/1878-password-policy-unicode-boundary.md) | P2 | 补证 | security | 6,14,18,40 |
| [1879 核对 Policy 编译副本与失败前的 writer 归属](../done/1879-headers-policy-writer-untouched.md) | P2 | 补证 | security | 14,20,34,40 |
| [1881 核对缓存写锁等待期间取消后的副作用](../done/1881-cache-queued-context-cancel.md) | P2 | 补证 | store | 26,28,31,32,41 |
| [1882 核对 KV 快照对未知字段及重复成员的兼容边界](../done/1882-kv-snapshot-duplicate-unknown-field.md) | P2 | 补证 | store | 6,18,22,41 |
| [1883 核对以更小容量重开后首次写入的持久边界](../done/1883-kv-resize-open-before-write.md) | P2 | 补证 | store | 28,30,31,41 |
| [1884 联测部分数据库句柄的清理与安全诊断](../done/1884-sql-open-partial-close-error-boundary.md) | P2 | 补证 | store | 22,25,29,30,41 |
| [1885 核对 Strict 单行读取中回调取消后的清理](../done/1885-sql-strict-empty-resultsets.md) | P2 | 补证 | store | 22,26,29,30,41 |
| [1886 核对 File 快照时间指针与多次 Clone 的归属](../done/1886-file-clone-shared-source-times.md) | P2 | 补证 | store | 6,29,34,41 |
| [1887 补证幂等完成入口对边界到期与状态的处理顺序](../done/1887-idempotency-expiry-status-precedence.md) | P2 | 补证 | store | 6,14,22,31,41 |
| [1889 补证就绪 Clone 的 nil 和空组件 map 语义](../done/1889-readiness-nil-empty-map-shape.md) | P2 | 补证 | health | 6,16,34,42 |
| [1890 确定 Tracker Reset 与在途检查的发布边界](../done/1890-tracker-reset-during-probe.md) | P2 | 补证 | x/observability/ops | 28,31,32,42 |
| [1891 核对健康集合变更与批量探测结果的代际边界](../done/1891-manager-registry-during-batch.md) | P2 | 补证 | x/observability/ops | 28,31,32,42 |
| [1892 联测年龄与备份数量同时设置时的活动保护](../done/1892-retention-age-backup-link-combined.md) | P2 | 补证 | log | 14,29,31,43 |
| [1893 核对底层短写后的诊断与后续完整记录](../done/1893-logger-short-record-followup.md) | P2 | 补证 | log | 22,25,29,43 |
| [1895 核对空名称记录的总量与系列计数](../done/1895-metrics-blank-name-record-counters.md) | P2 | 补证 | metrics | 6,14,32,44 |
| [1896 核对三路统计在同名饱和后的聚合顺序](../done/1896-metrics-merge-saturated-name-overlap.md) | P2 | 补证 | metrics | 6,32,34,44,46 |
| [1899 量化深路径分派在池边界的分配与生命周期](../done/1899-route-path-pool-size-bound-benchmark.md) | P2 | 补证 | router | 29,34,35,37,48 |
| [1859 补证 DefaultConfig 的重复调用与依赖选择](../done/1859-constructor-config-isolation.md) | P3 | 补证 | core | 3,5,6,14,36 |
| [1864 核对 Print 快照期间注册与失败后的重新打印](../done/1864-print-metadata-registration-during-snapshot.md) | P3 | 补证 | router | 6,16,29,34,37 |
| [1880 补证合成时钟回拨后的 RetryAfter 与 Reset](../done/1880-limiter-nonmonotonic-time-reset.md) | P3 | 补证 | security | 14,28,32,40 |
| [1888 核对组件健康模型 Enabled 的零值序列化](../done/1888-component-health-json-enabled-zero.md) | P3 | 补证 | health | 6,16,38,42 |
| [1894 核对 ErrorOutput 分流与衍生字段组合](../done/1894-json-error-sink-mutation-isolation.md) | P3 | 补证 | log | 5,16,25,34,43 |
| [1897 核对门面 Param 的 nil 请求和普通元数据](../done/1897-facade-state-nil-request-boundary.md) | P3 | 补证 | 根门面 | 3,6,27,37 |

## 验证记录

| 命令或范围 | 结果 |
|---|---|
| `make fmt`、`make fmt-check`、`git diff HEAD --check` | 通过。 |
| `make validate-diff-dry`、`make validate-diff` | 跨模块配置执行 `make gates`。全量 Go、边界、稳定 API、vet、race、参考模块和 CLI 通过。 |
| `make test`、`make lint`、`make stable-coverage` | 通过。lint 0 项，稳定模块语句覆盖率 96.8%。 |
| 健康宿主、并发 Shutdown 和 singleflight 的定向 race 回归 | 通过。关键回归连续运行 10 次。 |
| `make website-sync`、`make website-gates` | 通过，生成数据无差异，构建 184 页。构建仍有既有 `:global` CSS 压缩警告。 |
| 逐卡 `make check-card C=done/<卡名>` 与链接核验 | 52 张卡、52 个基线检查号和 417 条链接通过。 |
| 日志包 `GOOS=windows` 与 `GOOS=plan9` 的 amd64 `go test -c` | 均交叉编译成功，未执行原生平台测试。 |

## 本机基准

Go 1.26.0、darwin/arm64、Apple M1 Pro。每项三次采样并取中位数。路由冷样本在每次调用前清空缓存，热样本复用缓存，二者读取同一个匹配参数。留存样本测量复制整份历史的开销。

```sh
go test -p 1 ./router ./x/observability/ops/healthhttp -run '^$' -bench '^Benchmark(RoutePathPoolBoundary|HistoryUnsupportedFormat)$' -benchmem -benchtime=100ms -count=3
```

| 场景 | ns/op | B/op | allocs/op |
|---|---:|---:|---:|
| `BenchmarkRoutePathPoolBoundary/segments=63/warm=false` | 1854 | 3386 | 10 |
| `BenchmarkRoutePathPoolBoundary/segments=63/warm=true` | 240 | 880 | 3 |
| `BenchmarkRoutePathPoolBoundary/segments=64/warm=false` | 1715 | 3450 | 10 |
| `BenchmarkRoutePathPoolBoundary/segments=64/warm=true` | 291 | 944 | 3 |
| `BenchmarkRoutePathPoolBoundary/segments=65/warm=false` | 1773 | 3450 | 10 |
| `BenchmarkRoutePathPoolBoundary/segments=65/warm=true` | 237 | 944 | 3 |
| `BenchmarkHistoryUnsupportedFormat/0` | 1695 | 2195 | 18 |
| `BenchmarkHistoryUnsupportedFormat/10000` | 1757 | 2196 | 18 |
| `BenchmarkTrackerHistoryRetention/100` | 3772 | 11072 | 101 |
| `BenchmarkTrackerHistoryRetention/10000` | 443418 | 1044736 | 10001 |

历史导出比较使用 Go 文件覆盖载入 a8c2f28a 的 history.go，在同机运行同一基准。

| 历史条目数 | 基线 B/op | 当前 B/op |
|---:|---:|---:|
| 0 | 2195 | 2195 |
| 10000 | 3588581 | 2196 |
