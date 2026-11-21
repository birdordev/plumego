# 稳定模块质量复审实施记录

以 2b94cf86 为基线，已处理并归档 **53 张卡**。表中的优先级和证据状态保留审查时的分类。四张分析卡保存当前行为、验证方法和后续范围。 [基线报告](../../../docs/evidence/stable-quality-review-2b94cf86.md)

| 卡片 | 优先级 | 证据状态 | 主模块 | 第 9 节检查号 |
|---|---|---|---|---|
| [1900 探测异常后健康报告仍返回已过时的健康状态](../done/1900-aborted-batch-cached-health-publication.md) | P1 | 已复现 | x/observability/ops | 22,28,31,32,42,46 |
| [1901 奇数样本窗口把全成功组件标成改善](../done/1901-trend-odd-window-denominators.md) | P2 | 已复现 | x/observability/ops | 6,12,42,44,46 |
| [1902 历史统计按追加位置推算时间，跨度可能为负](../done/1902-history-stats-chronological-extrema.md) | P2 | 已复现 | x/observability/ops | 6,31,34,42,46 |
| [1903 取消的空健康批次丢失就绪诊断原因](../done/1903-readiness-cancellation-fixed-reason.md) | P2 | 已复现 | x/observability/ops | 22,26,28,42,46 |
| [1904 gzip 在底层不支持 Flush 时提前提交并关闭压缩选择](../done/1904-gzip-explicit-flush-capability-check.md) | P2 | 已复现 | middleware | 17,23,24,39,46 |
| [1905 一条历史记录的分页请求分配整个匹配集合](../done/1905-history-small-page-matching-set-allocation.md) | P2 | 源码确认 | x/observability/ops | 31,34,35,42,48 |
| [1907 核对空服务并发准备时诊断只属首次发布](../done/1907-prepare-empty-route-warning-single-publisher.md) | P2 | 补证 | core | 28,32,33,36,47 |
| [1908 核对多组件停止失败与重试的错误顺序](../done/1908-lifecycle-retry-stop-errors-order.md) | P2 | 补证 | core | 22,28,30,31,36 |
| [1909 核对 App 子组路由选项对同一应用的重入](../done/1909-route-option-nested-group-reentrant.md) | P2 | 补证 | core | 19,32,33,36 |
| [1911 核对最后一个 Start 回调结束时触发取消的回滚](../done/1911-bootstrap-cancel-partial-final-component.md) | P2 | 补证 | core | 26,28,30,31,36 |
| [1912 核对重复命名失败后的热分派与元数据](../done/1912-named-route-meta-reject-keeps-hot-match.md) | P2 | 补证 | router | 19,31,32,37 |
| [1913 核对空静态文件在不同 HTTP 方法与条件组合中的终态](../done/1913-static-regular-file-empty-range-matrix.md) | P2 | 补证 | router | 23,24,37,46 |
| [1914 核对 HEAD Reader 中途失败时已消费字节和头快照](../done/1914-head-failed-readfrom-retained-metadata.md) | P2 | 补证 | router | 23,24,37 |
| [1916 核对多层静态与参数落空后的通配参数归属](../done/1916-matcher-late-wildcard-after-two-dead-ends.md) | P2 | 补证 | router | 29,34,37,46 |
| [1917 核对编码成功后 writer 的短写与原始错误优先级](../done/1917-response-short-write-error-cause.md) | P2 | 补证 | contract | 21,22,23,38 |
| [1918 核对多字节 Request ID 裁剪后的接受长度与上下文继承](../done/1918-request-id-utf8-byte-boundary.md) | P2 | 补证 | contract | 6,18,25,27,38 |
| [1919 核对 RouteState 从内联到溢出后的参数覆盖](../done/1919-route-state-duplicate-key-overflow.md) | P2 | 补证 | contract | 6,27,34,35,38 |
| [1921 核对失败详情降级与底层业务头和短写的组合](../done/1921-details-fallback-after-existing-header.md) | P2 | 补证 | contract | 21,22,23,38 |
| [1922 核对 CORS 构建后独立持有暴露头和方法配置](../done/1922-cors-detached-expose-allow-options.md) | P2 | 补证 | middleware | 14,20,32,34,39 |
| [1923 核对超时回放容量失败后旧 Trailer 的出口归属](../done/1923-timeout-trailer-after-buffer-limit.md) | P2 | 补证 | middleware | 23,24,35,39,46 |
| [1924 核对已选错误状态后读取超限时的正文归属](../done/1924-bodylimit-keep-explicit-error-after-late-overrun.md) | P2 | 补证 | middleware | 20,23,24,32,39 |
| [1925 核对观测中间件与 requestid 的顺序和同一 ID](../done/1925-requestid-multi-layer-id-owner.md) | P2 | 补证 | middleware | 17,25,27,39 |
| [1926 核对 singleflight 达到捕获上限时 leader 与 waiter 的头部归属](../done/1926-singleflight-final-large-response-header-delta.md) | P2 | 补证 | middleware | 20,23,24,35,39 |
| [1927 核对 Debug 已转发的 Trailer 在异常退出时的响应归属](../done/1927-debug-passthrough-trailer-before-panic.md) | P2 | 补证 | middleware | 23,24,29,39 |
| [1928 核对恢复前底层写失败是否仍保留选定终态](../done/1928-recovery-writer-error-no-rewrite.md) | P2 | 补证 | middleware | 22,23,25,39 |
| [1929 核对合规 tracer 保持父取消及 trace 元数据](../done/1929-trace-parent-derived-context-on-early-return.md) | P2 | 补证 | middleware | 26,27,33,39 |
| [1930 核对低正速率下精确的饱和 RetryAfter 与 Reset](../done/1930-limiter-finite-duration-clamping.md) | P2 | 补证 | security | 14,22,35,40 |
| [1931 核对内存 KeyStore 读锁等待取消后的返回所有权](../done/1931-keystore-read-cancel-declines-copy.md) | P2 | 补证 | security | 22,26,34,40 |
| [1933 核对持久密码字段解码失败的固定错误分类](../done/1933-password-decoded-field-size-matrix.md) | P2 | 补证 | security | 18,22,25,35,40 |
| [1934 核对普通 Header 名值检查的空值与多字节边界](../done/1934-public-input-headers-byte-contract.md) | P2 | 补证 | security | 6,18,22,40 |
| [1935 核对无定期清理情况下过期项的内存配额](../done/1935-cache-expired-other-key-capacity.md) | P2 | 补证 | store | 14,28,31,35,41 |
| [1937 核对 data dir 使用普通符号链接别名时的锁归属](../done/1937-kv-snapshot-lock-symlink-alias.md) | P2 | 补证 | store | 22,28,29,41,47 |
| [1938 核对快照提交失败回滚后 TTL 的可见性](../done/1938-kv-persist-retry-expiring-snapshot.md) | P2 | 补证 | store | 28,30,31,32,41 |
| [1939 核对 ScanRows 扫描失败时迭代部分结果及资源保留](../done/1939-sql-scan-error-with-close-cause.md) | P2 | 补证 | store | 22,29,30,41 |
| [1940 核对幂等记录快照保留 nil 与显式空响应](../done/1940-idempotency-record-response-empty-copy.md) | P2 | 补证 | store | 6,16,34,41 |
| [1941 核对文件错误分类和路径省略组合](../done/1941-file-error-typed-cause-path-optional.md) | P2 | 补证 | store | 6,22,25,41 |
| [1943 核对组件健康快照的 Dependencies 容量和宿主转发边界](../done/1943-component-clone-dependency-handoff.md) | P2 | 补证 | health | 5,16,29,34,42 |
| [1944 核对手工就绪决定与自然探测报告的关系](../done/1944-report-manual-readiness-ownership.md) | P2 | 补证 | x/observability/ops | 6,28,31,42 |
| [1945 核对 Tracker 单组件探测被替换时的统计归属](../done/1945-direct-tracker-superseded-statistics.md) | P2 | 补证 | x/observability/ops | 28,31,32,42,44 |
| [1946 核对运行中重新启用大小轮换的计数起点](../done/1946-rotation-reenable-tracked-bytes.md) | P2 | 补证 | log | 14,28,29,43 |
| [1947 核对同一修改时间的备份淘汰与活动链接优先级](../done/1947-retention-equal-mtime-archive-choice.md) | P2 | 补证 | log | 16,29,31,43 |
| [1949 核对自定义 JSON field 编码回调次数与派生隔离](../done/1949-json-per-field-marshal-once.md) | P2 | 补证 | log | 25,33,34,43 |
| [1950 核对 HTTP Record 的秒值、纳秒时长和标签归属](../done/1950-http-record-duration-precise-label-ownership.md) | P2 | 补证 | metrics | 6,9,34,44 |
| [1951 补证 Clear 并行时只发布完整的 Base 统计快照](../done/1951-base-collector-branch-read-clear-handoff.md) | P2 | 补证 | metrics | 28,32,34,44,47 |
| [1906 历史导出说明缺少分页默认值与超界归一化](../done/1906-history-pagination-contract-doc.md) | P3 | 源码确认 | x/observability/ops | 6,14,16,42,52 |
| [1910 核对准备后混合零值和显式超时的配置透传](../done/1910-public-timeout-fields-zero-and-custom.md) | P3 | 补证 | core | 6,14,16,36 |
| [1915 核对未初始化和空组的命名路由快照](../done/1915-namedroutes-empty-map-isolation.md) | P3 | 补证 | router | 6,16,34,37 |
| [1920 核对 TraceContext 的 ParentSpanID 可选指针复制](../done/1920-request-metadata-empty-trace-parent.md) | P3 | 补证 | contract | 6,16,27,34,38 |
| [1932 核对 CSP Builder 多次 Build 的顺序和独立性](../done/1932-csp-deterministic-builder-snapshot.md) | P3 | 补证 | security | 5,14,16,34,40 |
| [1936 核对缓存键限制按字节与错误优先级](../done/1936-cache-multibyte-key-limit.md) | P3 | 补证 | store | 6,14,18,22,41 |
| [1942 核对未知状态随 Clone 和 JSON 编码保留](../done/1942-health-state-unknown-with-details.md) | P3 | 补证 | health | 6,16,34,38,42 |
| [1948 核对文本 ErrorOutput 的字段覆盖和两路输出顺序](../done/1948-text-error-output-parent-fields.md) | P3 | 补证 | log | 5,16,25,34,43 |
| [1952 核对根门面构造对 405 配置与默认行为的透传](../done/1952-facade-router-method-policy-forwarding.md) | P3 | 补证 | 根门面 | 3,5,6,37 |

## 验证记录

| 命令或范围 | 结果 |
|---|---|
| `make fmt`、`git diff HEAD --check` | 通过。 |
| `make validate-diff-dry`、`make validate-diff` | 跨模块配置执行完整 `make gates`。稳定 API、边界、vet、race、参考模块与 CLI 通过。 |
| `make test`、`make lint`、`make stable-coverage` | 通过。lint 0 项，九个稳定模块语句覆盖率 96.8%。 |
| 健康宿主、gzip 与稳定模块的回归 | 通过。关键并发和行为用例以 race 模式连续运行十次。五个修复用例在 Go overlay 载入基线源码后按预期失败。 |
| `make website-sync`、`make website-gates` | 通过，生成数据无差异，构建 184 页。构建有既有的 `:global` CSS 压缩警告。 |
| `make check-card C=done/<卡名>`、路径及行号核验 | 53 张卡、52 个检查编号及 408 条链接通过。 |
| 日志包的 Windows 与 Plan 9 amd64 交叉编译 | 两个 `go test -c` 均通过。原生运行未验证。 |

## 历史小页基准

同一公开接口、同一 Go 1.26.0 darwin/arm64 工作机，输入为无外部依赖的合成历史。下表取三次采样中位数，每页只读取一条并返回完整命中总数。

```sh
go test ./x/observability/ops/healthhttp -run '^$' -bench '^BenchmarkHistoryFirstSmallPage$' -benchmem -benchtime=100ms -count=3
```

| 匹配条目数 | 基线 ns/op | 当前 ns/op | 基线 B/op | 当前 B/op | 基线 allocs/op | 当前 allocs/op |
|---:|---:|---:|---:|---:|---:|---:|
| 100 | 2927 | 401 | 24432 | 96 | 8 | 1 |
| 10000 | 872575 | 34566 | 3579767 | 96 | 19 | 1 |
