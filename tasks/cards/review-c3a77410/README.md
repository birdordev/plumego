# 稳定模块第 9 节复审实施索引

基线为 `c3a77410`。编号 2008–2063，共 **56 张**。4 张处理已复现的行为问题，2 张处理源码确认的契约说明缺口，50 张用于定向补证。 [复审报告](../../../docs/evidence/stable-quality-review-c3a77410.md)

56 张任务卡已完成并归档。4 项行为修复、2 项契约说明、46 张定向验证和 4 张仅分析卡均有实施记录。

| 卡片 | 优先级 | 证据 | 主模块 | 第 9 节检查号 | 依赖 |
|---|---|---|---|---|---|
| [2008 空 HEAD 响应自动声明零长度](../done/2008-head-empty-handler-length-parity.md) | P2 | 已复现 | `router` | 6,17,24,37,45 | 无 |
| [2009 固定时刻下轮换复用活动文件](../done/2009-rotation-distinct-target-at-equal-clock.md) | P2 | 已复现 | `log` | 14,28,29,31,35,43,45 | 无 |
| [2010 基础统计越上界后返回负值](../done/2010-base-nonnegative-saturated-counter.md) | P2 | 已复现 | `metrics` | 6,32,44,45 | 无 |
| [2011 健康报告组件列表顺序漂移](../done/2011-stable-report-component-order.md) | P2 | 已复现 | `x/observability/ops` | 6,16,34,42,45 | 无 |
| [2014 未 Serve 前关闭再尝试启动](../done/2014-shutdown-prepared-but-not-served.md) | P2 | 补证 | `core` | 28,31,36,46 | 无 |
| [2015 装配中并行 Prepare 与 Server](../done/2015-second-prepare-during-blocked-assembly.md) | P2 | 补证 | `core` | 5,28,32,36,47 | 无 |
| [2016 就绪回调异常退出释放生命周期阶段](../done/2016-ready-goexit-releases-phase.md) | P2 | 补证 | `core` | 28,29,33,36,47 | 无 |
| [2017 第一次 Shutdown 取消后再次排空](../done/2017-shutdown-resumes-after-partial-context.md) | P2 | 补证 | `core` | 26,28,29,36,47 | 无 |
| [2019 显式方法分支落空后回到完整 Any](../done/2019-any-complete-after-explicit-dead-end.md) | P2 | 补证 | `router` | 19,37,45,46 | 无 |
| [2020 显式零长度 HEAD 保持声明](../done/2020-head-declared-zero-length.md) | P2 | 补证 | `router` | 6,24,37,45 | 2008 |
| [2021 StaticFS Stat 失败仍归还文件](../done/2021-static-file-stat-failure-close.md) | P2 | 补证 | `router` | 22,29,37,46 | 无 |
| [2024 编码回退时保留底层写入错误](../done/2024-error-fallback-sink-failure-chain.md) | P2 | 补证 | `contract` | 21,22,23,38,46 | 无 |
| [2025 成功响应 Meta 失败时保持未提交](../done/2025-response-meta-marshal-precommit.md) | P2 | 补证 | `contract` | 6,21,23,38,46 | 无 |
| [2026 多分支 APIError 的选择次序](../done/2026-joined-api-error-traversal-order.md) | P2 | 补证 | `contract` | 6,22,38,46 | 无 |
| [2028 Timeout 两种预算次序和精确边界](../done/2028-timeout-replay-and-buffer-limits-at-boundary.md) | P2 | 补证 | `middleware/timeout` | 14,23,24,35,39,46 | 无 |
| [2029 Debug 已透传时的写失败及尾字段](../done/2029-debug-flushed-trailer-with-ordinary-failure.md) | P2 | 补证 | `middleware/debug` | 23,24,39,46 | 无 |
| [2031 压缩协商与部分内容的表示一致性](../done/2031-gzip-range-success-representation-parity.md) | P2 | 补证 | `middleware/compression` | 17,23,24,39 | 无 |
| [2032 关闭请求头注入时的传播所有权](../done/2032-requestid-exclude-request-header-isolation.md) | P2 | 补证 | `middleware/requestid` | 6,18,27,34,39 | 无 |
| [2033 CORS 已含多项 Vary 时合并](../done/2033-cors-vary-before-finalized-success.md) | P2 | 补证 | `middleware/cors` | 20,23,34,39 | 无 |
| [2034 排队期限与上游提前结束](../done/2034-concurrency-limit-queued-context-deadline.md) | P2 | 补证 | `middleware/concurrencylimit` | 20,26,28,35,39,47 | 无 |
| [2035 合流重放保留重复响应头](../done/2035-singleflight-replay-header-multiplicity.md) | P2 | 补证 | `middleware/singleflight` | 6,23,24,34,39 | 无 |
| [2036 指标观察底层短写实际字节](../done/2036-httpmetrics-short-writer-observed-bytes.md) | P2 | 补证 | `middleware/httpmetrics` | 23,24,39,44,46 | 无 |
| [2038 密钥存储预取消更新保留原快照](../done/2038-keystore-cancel-replacement-preserves-value.md) | P2 | 补证 | `security/jwt` | 26,31,34,40 | 无 |
| [2039 安全策略编译后与配置副本隔离](../done/2039-compiled-headers-independence-from-source.md) | P2 | 补证 | `security/headers` | 14,34,40,45 | 无 |
| [2040 密码成本参数边界及分类](../done/2040-password-cost-boundary-contract.md) | P2 | 补证 | `security/password` | 14,22,35,40,46 | 无 |
| [2042 限流拒绝与进程内计数一致](../done/2042-abuse-reject-metrics-counter-parity.md) | P2 | 补证 | `security/abuse` | 28,32,40,44 | 无 |
| [2044 KV 预取消写入保留持久快照](../done/2044-kv-precommit-context-cancel-keeps-reopen-state.md) | P2 | 补证 | `store/kv` | 26,31,34,41,46 | 无 |
| [2045 KV Keys 快照及排序与删除隔离](../done/2045-kv-keys-snapshot-after-concurrent-write.md) | P2 | 补证 | `store/kv` | 31,32,34,41,47 | 无 |
| [2046 快照写失败时保留 Close 错误分类](../done/2046-kv-replace-close-and-write-error-order.md) | P2 | 补证 | `store/kv` | 22,29,30,31,41,46 | 无 |
| [2047 缓存 Append 对输入和返回切片隔离](../done/2047-cache-append-copies-caller-buffer.md) | P2 | 补证 | `store/cache` | 32,34,41,45 | 无 |
| [2048 缓存 Decr 边界错误保留值和 TTL](../done/2048-cache-decrement-boundary-atomic.md) | P2 | 补证 | `store/cache` | 22,28,31,41,46 | 无 |
| [2049 ScanRows 中途扫描和关闭失败](../done/2049-sql-scanrows-primary-close-error.md) | P2 | 补证 | `store/db` | 22,29,30,41,46 | 无 |
| [2051 完成校验的精确到期点](../done/2051-idempotency-completion-expiry-inclusive.md) | P2 | 补证 | `store/idempotency` | 14,22,31,41,46 | 无 |
| [2055 轮换新文件名能参与既有保留策略](../done/2055-rotation-new-filename-under-retention.md) | P2 | 补证 | `log` | 6,29,35,43,45 | 2009 |
| [2056 派生 JSON logger 保留错误 writer 分流](../done/2056-json-derived-error-writer-ownership.md) | P2 | 补证 | `log` | 5,32,34,43 | 无 |
| [2058 饱和边界下 HTTP 状态分类仍独立](../done/2058-http-observer-saturation-at-status-threshold.md) | P2 | 补证 | `metrics` | 32,44,45,46 | 2010 |
| [2059 聚合清空遇子回调异常的边界](../done/2059-aggregate-clear-child-panic-order.md) | P2 | 补证 | `metrics` | 28,31,33,44,46 | 无 |
| [2061 并发注册与健康报告快照一致性](../done/2061-report-snapshot-consistency-with-registry-churn.md) | P2 | 补证 | `x/observability/ops` | 28,32,34,42,47 | 无 |
| [2062 关闭后显式批检查的历史记录语义](../done/2062-closed-batch-tracker-history-contract.md) | P2 | 补证 | `x/observability/ops` | 16,28,31,35,42 | 无 |
| [2063 直查与批次跨 Reset 的计量边界](../done/2063-tracker-direct-and-batch-reset-boundary.md) | P2 | 补证 | `x/observability/ops` | 28,31,32,42,47 | 无 |
| [2012 说明 LastStatus 的时间语义](../done/2012-document-last-observed-component-attempt.md) | P3 | 源码确认 | `x/observability/ops` | 6,16,31,42 | 无 |
| [2013 应用 URL 入口补齐参数约束说明](../done/2013-app-url-document-router-validation.md) | P3 | 源码确认 | `core` | 6,16,37,52 | 无 |
| [2018 冻结前后 RouteInfo 快照隔离](../done/2018-group-route-list-freeze-copy.md) | P3 | 补证 | `core` | 19,34,36 | 无 |
| [2022 反查路径的字面查询分隔符往返](../done/2022-reverse-static-literal-query-roundtrip.md) | P3 | 补证 | `router` | 6,18,37,45 | 无 |
| [2023 Print 失败 writer 的调用边界](../done/2023-route-print-failing-sink.md) | P3 | 补证 | `router` | 16,22,29,37 | 无 |
| [2027 TraceContext 空 Baggage 快照形状](../done/2027-trace-nil-versus-empty-baggage.md) | P3 | 补证 | `contract` | 6,27,34,38 | 无 |
| [2030 BodyLimit 禁用时保留请求身份](../done/2030-bodylimit-disabled-keeps-request-identity.md) | P3 | 补证 | `middleware/bodylimit` | 14,17,20,39 | 无 |
| [2037 Tracing 结束时观察到派生上下文](../done/2037-trace-finalizer-context-after-next.md) | P3 | 补证 | `middleware/tracing` | 20,26,27,39 | 无 |
| [2041 零 Principal 多次读取的独立性](../done/2041-authn-zero-principal-ownership.md) | P3 | 补证 | `security/authn` | 27,34,40,45 | 无 |
| [2043 普通邮箱标签边界契约](../done/2043-input-email-domain-length-boundary.md) | P3 | 补证 | `security/input` | 14,18,40,46 | 无 |
| [2050 文件克隆指针时间戳独立](../done/2050-file-clone-timestamps-are-independent.md) | P3 | 补证 | `store/file` | 16,34,41,45 | 无 |
| [2052 健康时间戳时区及瞬时值往返](../done/2052-status-json-time-zone-roundtrip.md) | P3 | 补证 | `health` | 6,16,38,42 | 无 |
| [2053 健康依赖列表改写与扩容隔离](../done/2053-status-clone-dependencies-capacity.md) | P3 | 补证 | `health` | 34,42,45 | 无 |
| [2054 未就绪组件 false 在 JSON 中保留](../done/2054-readiness-json-false-component.md) | P3 | 补证 | `health` | 6,38,42,45 | 无 |
| [2057 文本多层字段覆盖优先级](../done/2057-text-formatter-inherited-field-precedence.md) | P3 | 补证 | `log` | 6,34,43 | 无 |
| [2060 门面默认配置多次读取独立](../done/2060-facade-default-config-independence.md) | P3 | 补证 | 根门面 | 3,6,14,36 | 无 |
