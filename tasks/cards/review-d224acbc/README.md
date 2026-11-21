# 稳定模块质量复审实施记录

已执行并归档 **54 张**，编号 1743–1796。三类健康宿主行为和两条 core 诊断路径的变更已同步模块说明。缓存 Close 释放数据，Stats 保留最后使用量。日志初始化复用先准备后替换。依据为 [基线审查报告](../../../docs/evidence/stable-quality-review-d224acbc.md)。

表中优先级和证据状态记录初始取证，每张归档卡附实施结论与验收。

| 卡片 | 优先级 | 证据状态 | 主模块 | 第 9 节检查号 |
|---|---|---|---|---|
| [1743 单组件失败探测改写 Manager 持有的快照](../done/1743-component-snapshot-ownership.md) | P1 | 已复现 | x/observability | 6,23,34,42,45 |
| [1744 单组件入口解引用存在但为空的快照](../done/1744-component-nil-snapshot.md) | P1 | 已复现 | x/observability | 6,20,21,42,46 |
| [1745 无路由告警使已经发布的 Prepare 抛出 panic](../done/1745-prepare-warning-after-publication.md) | P1 | 已复现 | core | 22,28,31,33,36 |
| [1746 后台排水诊断无回调 panic 边界](../done/1746-drain-diagnostic-panic.md) | P1 | 源码确认 | core | 29,33,36,46 |
| [1747 初始化文件日志先删除活动链接](../done/1747-init-link-publication.md) | P2 | 源码确认 | log | 29,30,31,43 |
| [1748 关闭的缓存仍保留已不可读取的全部条目](../done/1748-cache-closed-retention.md) | P2 | 源码确认 | store | 6,28,29,34,35 |
| [1749 内存 JWT KeyStore 遍历并分配过期项容量](../done/1749-keystore-expired-capacity.md) | P2 | 源码确认 | security | 14,29,35,40,48 |
| [1752 核对 TLS 下显式禁用 HTTP/2 的实际协商](../done/1752-http2-explicit-disable-serve-tls.md) | P2 | 补证 | core | 6,14,17,36 |
| [1753 核对 Ready 被 Goexit 终止后的阶段恢复](../done/1753-ready-goexit-restores-phase.md) | P2 | 补证 | core | 28,29,33,36,47 |
| [1754 核对 handler 先发布后 TLS 准备失败的重试](../done/1754-frozen-config-tls-retry.md) | P2 | 补证 | core | 28,31,36 |
| [1755 核对最后连接关闭与排水 ticker 同时就绪](../done/1755-drain-last-connection-tick.md) | P2 | 补证 | core | 28,32,36,47 |
| [1756 验证本地静态根的生命周期与边界约定](../done/1756-static-open-root-ownership.md) | P2 | 补证 | router | 16,29,37,46 |
| [1757 核对 HEAD 逻辑状态与连接接管顺序](../done/1757-head-hijack-pending-status.md) | P2 | 补证 | router | 23,24,37 |
| [1758 核对缓存逐出与 in-flight 路由上下文](../done/1758-route-observer-cache-eviction.md) | P2 | 补证 | router | 29,32,34,37 |
| [1759 补证静态文件 rewind 失败的单独终态](../done/1759-static-rewind-error-terminal.md) | P2 | 补证 | router | 22,23,29,37 |
| [1761 验证响应 request_id 与错误自带 ID 的优先级](../done/1761-error-request-id-precedence.md) | P2 | 补证 | contract | 6,21,27,38 |
| [1762 补证错误详情中同源切片视图的所有权](../done/1762-error-details-shared-view.md) | P2 | 补证 | contract | 34,38,45 |
| [1763 核对不合法错误状态对 HTTP 及 JSON 的统一映射](../done/1763-error-204-explicit-normalization.md) | P2 | 补证 | contract | 6,21,22,38 |
| [1764 补证 trace flags 与 baggage 更新时的快照](../done/1764-trace-flag-baggage-isolation.md) | P2 | 补证 | contract | 27,32,34,38 |
| [1765 核对成功编码短写前业务头和内容类型](../done/1765-response-writer-header-owner.md) | P2 | 补证 | contract | 21,23,24,38 |
| [1766 核对重复使用同一并发限制 builder 的配额范围](../done/1766-concurrency-shared-builder-pool.md) | P2 | 补证 | middleware | 5,14,29,39 |
| [1767 核对读到超限后上下文取消的优先级](../done/1767-bodylimit-read-pending-context.md) | P2 | 补证 | middleware | 23,26,29,39 |
| [1768 补证观察阶段的访问日志时间与字段来源](../done/1768-accesslog-detached-timing.md) | P2 | 补证 | middleware | 16,25,27,39 |
| [1769 锁定 Debug 已选错误状态的拒绝 Flush 行为](../done/1769-debug-explicit-status-flush.md) | P2 | 补证 | middleware | 23,24,39 |
| [1770 验证关联 ID 诊断解码的整数边界](../done/1770-requestid-uint64-boundary.md) | P2 | 补证 | middleware | 6,18,22,39 |
| [1771 核对 tracer 派生 Context 的 deadline 传递](../done/1771-trace-starter-context-deadline.md) | P2 | 补证 | middleware | 26,27,33,39 |
| [1772 核对合并方法配置切片的复制边界](../done/1772-singleflight-method-config-snapshot.md) | P2 | 补证 | middleware | 14,20,34,39 |
| [1773 核对压缩在 304 和 Range 交叠时的变体头](../done/1773-compression-bodyless-range-mix.md) | P2 | 补证 | middleware | 21,23,24,39 |
| [1774 测量注入时钟同步扫描对请求成本的影响](../done/1774-limiter-request-scan-cost.md) | P2 | 补证 | security | 29,32,35,40,48 |
| [1775 核对已取消验证与普通无效输入分类](../done/1775-jwt-canceled-verify-config.md) | P2 | 补证 | security | 22,26,40,46 |
| [1776 核对多项头策略配置失败的诊断分类](../done/1776-headers-join-errors-stability.md) | P2 | 补证 | security | 14,22,25,40 |
| [1777 核对密码成本边界分类且不触发高成本计算](../done/1777-password-work-factor-edge.md) | P2 | 补证 | security | 14,25,35,40 |
| [1778 验证 KeyStore 过期读取和覆盖交错](../done/1778-keystore-expiry-index-snapshot.md) | P2 | 补证 | security | 26,31,32,40 |
| [1779 核对持久快照缺字段与空对象分类](../done/1779-kv-state-missing-vs-null.md) | P2 | 补证 | store | 22,31,41,46 |
| [1780 核对已分类读预算错误后的受限重试](../done/1780-kv-place-after-read-errors.md) | P2 | 补证 | store | 28,31,41 |
| [1781 核对缓存删除在取消和关闭同时发生时的判决](../done/1781-cache-delete-canceled-priority.md) | P2 | 补证 | store | 22,26,28,41 |
| [1782 补证严格查询跳过空结果集的行数契约](../done/1782-db-strict-empty-resultsets.md) | P2 | 补证 | store | 22,29,41 |
| [1783 核对 QueryRowContext 延迟返回的错误分类](../done/1783-db-query-row-lazy-scan-error.md) | P2 | 补证 | store | 6,22,26,41 |
| [1784 核对 PutOptions 与 File.Clone 的 metadata 生命周期](../done/1784-file-putoptions-metadata-source.md) | P2 | 补证 | store | 16,34,41 |
| [1785 锁定幂等完成校验在同一时刻的错误顺序](../done/1785-idempotency-completion-expiry-order.md) | P2 | 补证 | store | 6,22,28,41 |
| [1786 核对 HealthStatus.Clone 的时间、依赖和浅层详情归属](../done/1786-status-clone-timestamp-dependencies.md) | P2 | 补证 | health | 16,34,42 |
| [1787 核对健康模型 details 空值及普通值的线格式](../done/1787-standalone-health-empty-details-shape.md) | P2 | 补证 | health | 6,21,38,42 |
| [1788 补证自定义健康宿主 typed nil 注入行为](../done/1788-health-manager-nil-dependency-kind.md) | P2 | 补证 | x/observability | 5,15,36,42 |
| [1789 核对历史 CSV 写失败后的响应终态](../done/1789-health-history-csv-write-failure.md) | P2 | 补证 | x/observability | 22,23,29,42 |
| [1790 核对相同修改时间的归档文件保留策略](../done/1790-file-retention-tie-break.md) | P2 | 补证 | log | 6,31,43 |
| [1791 核对并发 Flush 与输出的同步边界](../done/1791-log-flush-writer-order.md) | P2 | 补证 | log | 29,31,32,43 |
| [1792 核对结构化字段的 typed nil 自定义编码](../done/1792-json-typed-nil-marshaler.md) | P2 | 补证 | log | 21,33,34,43 |
| [1793 核对聚合开始时间与空计数的组合](../done/1793-multi-starttime-empty-orders.md) | P2 | 补证 | metrics | 6,32,44 |
| [1794 核对子收集器回调失败的传播范围](../done/1794-metric-fanout-callback-stop-policy.md) | P2 | 补证 | metrics | 23,33,44,46 |
| [1795 补证高基数收集清空后的 map 归属与成本](../done/1795-metric-high-cardinality-reset.md) | P2 | 补证 | metrics | 34,35,44,48 |
| [1750 组件列表顺序随 map 遍历变化](../done/1750-component-list-order.md) | P3 | 源码确认 | x/observability | 6,16,34,42 |
| [1751 上一批审查报告有多余文件末尾空行](../done/1751-review-eof-whitespace.md) | P3 | 源码确认 | docs | 49,52 |
| [1760 核对反向路由参数数量错误的诊断范围](../done/1760-named-route-error-escaping.md) | P3 | 补证 | router | 16,22,37 |
| [1796 核对根门面显式零配置与默认配置的文档边界](../done/1796-facade-zero-config-prepare.md) | P3 | 补证 | 根门面 | 3,6,14,16 |


## 验证记录

| 命令或核验 | 结果 |
|---|---|
| `make fmt`、`make fmt-check` | 通过。 |
| `make validate-diff-dry`、`make validate-diff` | 跨模块配置执行 `make gates` 并通过。覆盖根模块 race、vet、边界、公开 API 快照、文档示例、参考程序和 CLI。 |
| `make test` | 根模块 20 秒配置通过。前一轮定向测试发现新缓存用例以同步测试等待 Mutex 无法推进，改用显式通道和上下文检查后，缓存定向 race 与全量门禁通过。 |
| `make lint`、`make stable-coverage` | golangci-lint v2.14.0 报告 0 项，稳定模块语句覆盖率 96.5%。 |
| `make website-gates`、`make website-check-generated` | 通过，网站构建 184 页，生成数据无差异。 |
| `GOOS=windows GOARCH=amd64 CGO_ENABLED=0 go test -c -o /tmp/plumego-d224-log-windows.test.exe ./log` | 交叉编译通过。 |
| `GOOS=plan9 GOARCH=amd64 CGO_ENABLED=0 go test -c -o /tmp/plumego-d224-log-plan9.test ./log` | 交叉编译通过。 |
| 逐张 `make check-card C=done/<卡片文件名>`，另核对编号、链接与 `git diff HEAD --check` | 54 张归档卡、52 个基线检查号和 407 条链接均已核验。 |

本轮日志链接没有 Windows 或 Plan 9 原生运行证据。

## 本机基准

Go 1.26.0、darwin/arm64、Apple M1 Pro。下表记录三次采样中位数。限流扫描和指标基数用于记录当前成本。

```sh
go test -p 1 ./security/jwt ./security/abuse ./metrics -run '^$' -bench 'Benchmark(KeyStoreKeysExpiryRatio|LimiterRequestCleanupCost|BaseCollectorNameCardinality)$' -benchmem -benchtime=100ms -count=3
```

| 场景 | ns/op | B/op | allocs/op |
|---|---:|---:|---:|
| `BenchmarkKeyStoreKeysExpiryRatio/0` | 55486.00 | 0 | 0 |
| `BenchmarkKeyStoreKeysExpiryRatio/32` | 58920.00 | 512 | 1 |
| `BenchmarkKeyStoreKeysExpiryRatio/4096` | 60367.00 | 65536 | 1 |
| `BenchmarkLimiterRequestCleanupCost/scan=false` | 98.12 | 0 | 0 |
| `BenchmarkLimiterRequestCleanupCost/scan=true` | 24604.00 | 0 | 0 |
| `BenchmarkBaseCollectorNameCardinality/1` | 385.30 | 688 | 7 |
| `BenchmarkBaseCollectorNameCardinality/1000` | 98246.00 | 163851 | 31 |


内存 KeyStore 共 4096 条。过期判定仍用同一个时间，所有场景均遍历条目。可见集较小时，暂存只分配可见键所需的切片。
全有效场景的分配量一致，耗时中位数从约 54.8 μs 到 60.4 μs。本轮采样只为核对分配量，耗时仍需在一致负载下复测。

| 可见键 | 修改前 B/op | 修改后 B/op |
|---|---:|---:|
| 0 | 65536 | 0 |
| 32 | 65536 | 512 |
| 4096 | 65536 | 65536 |
