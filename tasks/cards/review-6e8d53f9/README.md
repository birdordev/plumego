# 稳定模块第 9 节复审任务卡

基线为 `6e8d53f9`。共 25 张卡，25 张卡均已结案。各卡记录了实施或补证结果。 [审查报告](../../../docs/evidence/stable-quality-review-6e8d53f9.md)

实施次序以 **2119 → 2120 → 2121 → 2122** 为主。2135 在 2119 落地后完成取证和修复。

| 卡片 | 优先级 | 证据 | 所属模块 | 检查号 | 前置卡 |
|---|---|---|---|---|---|
| [2119 保留 gzip 外层响应头和既有编码](../done/2119-gzip-inherits-response-headers.md) | P1 | 已复现 | `middleware/compression` | 6,17,23,24,34,39,45 | 无 |
| [2120 阻止旧批次失败覆盖替换组件的就绪状态](../done/2120-stale-health-batch-readiness.md) | P1 | 已复现 | `x/observability/ops` | 26,28,31,32,42,45,47 | 无 |
| [2121 移出 KV 锁内的调用方上下文方法](../done/2121-kv-context-check-lock-boundary.md) | P1 | 源码确认 | `store/kv` | 26,30,31,32,33,41,46 | 无 |
| [2122 保留请求合并首个已提交响应的尾部字段](../done/2122-singleflight-committed-trailer-abnormal-exit.md) | P2 | 已复现 | `middleware/singleflight` | 17,23,24,29,39,45,46 | 无 |
| [2123 在生命周期准入中避免锁内调用上下文实现](../done/2123-lifecycle-start-context-lock-boundary.md) | P2 | 源码确认 | `core` | 26,28,32,33,36,47 | 无 |
| [2129 消除 KV 锁等待取消测试的固定延迟](../done/2129-kv-cancel-test-barrier.md) | P2 | 源码确认 | `store/kv` | 26,45,46,47 | 无 |
| [2124 核对请求合并对 101 与接管连接的处理](../done/2124-replay-switching-protocols.md) | P2 | 已复现并修复 | `middleware/singleflight` | 17,23,24,39,46 | 无 |
| [2127 量化 struct 字段绕过 JSON 容器预算的代价](../done/2127-log-json-struct-budget-baseline.md) | P2 | 待补证（已测量） | `log` | 6,16,35,43,48 | 无 |
| [2128 验证请求合并在途状态的资源上界](../done/2128-singleflight-inflight-capacity-baseline.md) | P2 | 待补证 | `middleware/singleflight` | 29,35,39,47,48 | 无 |
| [2130 核对 HEAD 与 Allow 在预热和冻结后的决策矩阵](../done/2130-router-head-freeze-method-matrix.md) | P2 | 待补证 | `router` | 19,23,24,32,37,45,47 | 无 |
| [2132 核对无正文 JSON 响应在 103 后的最终提交](../done/2132-contract-bodyless-response-commit-order.md) | P2 | 待补证 | `contract` | 17,21,23,24,38,45 | 无 |
| [2134 核对超时缓冲的 Content-Length 写入语义](../done/2134-timeout-content-length-write-parity.md) | P2 | 已复现并修复 | `middleware/timeout` | 17,22,23,24,31,39,46 | 无 |
| [2135 核对 gzip 包裹路由时 HEAD 的表示元数据](../done/2135-gzip-head-router-representation.md) | P2 | 已复现并修复 | `middleware/compression` | 6,17,23,24,37,39,45 | 2119 |
| [2136 确认 JWT 验证时间判定期间的取消口径](../done/2136-jwt-verification-clock-cancellation-contract.md) | P2 | 待补证 | `security/jwt` | 6,14,26,31,40,46 | 无 |
| [2138 核对多个 Close 调用者等待同一写入完成](../done/2138-cache-concurrent-close-waiters.md) | P2 | 待补证 | `store/cache` | 28,29,32,33,41,47 | 无 |
| [2139 核对取消与事务回滚错误的分类](../done/2139-sql-rollback-joined-causes.md) | P2 | 待补证 | `store/db` | 22,26,29,30,31,41,46 | 无 |
| [2125 合并重复的无名指标断言](../done/2125-metrics-unnamed-regression-overlap.md) | P3 | 源码确认 | `metrics` | 9,12,44,45,47 | 无 |
| [2126 明确 App 与组注册辅助函数的作用域](../done/2126-core-registration-helper-doc.md) | P3 | 源码确认 | `core` | 9,12,16,19,52 | 无 |
| [2131 核对非 seek 静态资源空文件的 GET 和 HEAD](../done/2131-router-static-head-sequential-empty.md) | P3 | 待补证 | `router` | 6,17,22,24,29,37 | 无 |
| [2133 测量错误详情共享子图的复制和编码开销](../done/2133-contract-details-graph-benchmark.md) | P3 | 待补证 | `contract` | 12,34,35,38,48 | 无 |
| [2137 核对认证主体覆盖与请求取消的组合](../done/2137-authn-context-snapshot-composition.md) | P3 | 待补证 | `security/authn` | 25,27,34,40,45 | 无 |
| [2140 核对关闭中的存活与就绪 HTTP 契约](../done/2140-health-liveness-readiness-shutdown-pair.md) | P3 | 待补证 | `x/observability/ops` | 17,21,28,42,45,46 | 无 |
| [2141 核对根门面准备失败后的直接分派](../done/2141-facade-prepare-failure-direct-dispatch.md) | P3 | 待补证 | `.` | 3,6,14,28,36,37 | 无 |
| [2142 核对 JSON 编码器异常后的后续同级记录](../done/2142-log-json-recovery-followup-record.md) | P3 | 待补证 | `log` | 25,29,32,43,45,46 | 无 |
| [2143 核对嵌套扇出中的标签隔离和独立重置](../done/2143-metrics-nested-fanout-labels.md) | P3 | 待补证 | `metrics` | 31,32,34,44,45,47 | 无 |
