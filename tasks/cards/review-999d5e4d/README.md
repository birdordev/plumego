# 稳定模块复审实施索引

基线为 `999d5e4d`。编号 1953–2007，共 **55 张**。完成 5 张修复及说明卡、47 张定向回归卡和 3 张仅分析卡。 [第 9 节复审报告](../../../docs/evidence/stable-quality-review-999d5e4d.md)

1953 的准入变更先落地，1954、2002、2003 的计量与兼容性随后通过。分析卡 1995 和 1998 记录合成边界观察，2000 核实逐子收集器重置的契约。

| 卡片 | 优先级 | 证据状态 | 主模块 | 第 9 节检查号 |
|---|---|---|---|---|
| [1953 未执行的单组件检查发布失败并挤掉有效检查](../done/1953-direct-health-cancel-before-admission.md) | P1 | 已复现 | x/observability/ops | 6,22,26,31,42,46,47 |
| [1954 单组件跟踪把被更新结果覆盖的成功计成失败](../done/1954-direct-tracker-attempt-metrics.md) | P2 | 已复现 | x/observability/ops | 6,31,42,44,46,47 |
| [1955 Debug 在外层恢复 panic 后丢失已提交响应的 trailer](../done/1955-debug-recovery-committed-trailer.md) | P2 | 已复现 | middleware/debug | 6,17,23,24,39,46 |
| [1956 重新启用文件轮换时漏算关闭期间写入的内容](../done/1956-file-rotation-reenable-size-accounting.md) | P2 | 已复现 | log | 32,34,43,46 |
| [1958 锁定 Run 已预备时的监听失败分类](../done/1958-run-address-in-use-classification.md) | P2 | 补证 | core | 15,28,36,46 |
| [1959 核对嵌套生命周期组的就绪错误身份](../done/1959-nested-lifecycle-ready-child-error-order.md) | P2 | 补证 | core | 11,22,26,36 |
| [1960 核对嵌套组空相对路径上的命名路由](../done/1960-group-empty-relative-named-route.md) | P2 | 补证 | core | 8,12,36,37 |
| [1962 核对重叠路由的 Allow 中 HEAD 只出现一次](../done/1962-allow-method-head-token-on-overlap.md) | P2 | 补证 | router | 6,13,37,45 |
| [1963 比对两种静态文件入口的 If-Range 行为](../done/1963-static-if-range-validator-parity.md) | P2 | 补证 | router | 6,12,17,37 |
| [1966 核对类型化错误对自定义状态与代码的优先级](../done/1966-typed-error-status-code-precedence.md) | P2 | 补证 | contract | 6,18,21,38 |
| [1967 核对 205 响应不会调用用户序列化逻辑](../done/1967-reset-content-skips-user-marshaler.md) | P2 | 补证 | contract | 17,23,24,38 |
| [1970 核对合法重复 CORS 头名称的输出](../done/1970-cors-preflight-duplicate-case-order.md) | P2 | 补证 | middleware/cors | 13,16,39,46 |
| [1971 核对限流 body Close 时底层错误一次交付](../done/1971-bodylimit-reader-close-error-ownership.md) | P2 | 补证 | middleware/bodylimit | 15,24,28,39 |
| [1972 核对异常 owner 完成后同键可以重新执行](../done/1972-singleflight-next-flight-after-owner-panic.md) | P2 | 补证 | middleware/singleflight | 24,28,34,39,47 |
| [1976 核对无排队配置下拒绝后的容量恢复](../done/1976-concurrency-limit-no-queue-slot-reuse.md) | P2 | 补证 | middleware/concurrencylimit | 26,34,35,39,47 |
| [1977 核对下游显式编码的流不会叠加 gzip](../done/1977-gzip-already-encoded-ordinary-flush.md) | P2 | 补证 | middleware/compression | 17,23,24,39 |
| [1978 核对零 TTL 重写会清除旧过期时间](../done/1978-keystore-zero-ttl-replaces-expiry.md) | P2 | 补证 | security/jwt | 31,34,40,47 |
| [1982 核对空键拒绝的桶计量](../done/1982-abuse-allow-key-empty-admission-stats.md) | P2 | 补证 | security/abuse | 28,34,35,40,44 |
| [1984 核对缓存替换值的容量和到期计量](../done/1984-cache-exact-capacity-replacement-with-expiry.md) | P2 | 补证 | store/cache | 31,34,35,41 |
| [1985 核对取消的 Clear 对条目数和内存计量无副作用](../done/1985-cache-clear-error-atomic-stats.md) | P2 | 补证 | store/cache | 22,26,34,41 |
| [1986 核对快照读取边界上的底层错误归属](../done/1986-kv-snapshot-exact-limit-with-reader-error.md) | P2 | 补证 | store/kv | 15,21,35,41,46 |
| [1987 核对删除不存在条目时的快照身份](../done/1987-kv-delete-noop-preserves-snapshot.md) | P2 | 补证 | store/kv | 28,31,34,41 |
| [1988 核对 ScanRow 无记录和 nil 输入的错误区分](../done/1988-db-scan-row-no-rows-classification.md) | P2 | 补证 | store/db | 15,18,21,41 |
| [1990 核对文件元数据副本中的重复容器所有权](../done/1990-file-typed-metadata-alias-ownership.md) | P2 | 补证 | store/file | 31,34,41,45 |
| [1995 查证文件轮换遇到已存在名称时的大小归属](../done/1995-file-rotation-existing-target-accounting.md) | P2 | 补证 | log | 32,34,43,46 |
| [1996 核对文本 writer 切换时的级别分流](../done/1996-text-writer-switch-with-stderr-split.md) | P2 | 补证 | log | 28,32,43,47 |
| [1999 核对子指标有 series 计数无名称映射时的合并](../done/1999-multi-active-series-child-only-stats.md) | P2 | 补证 | metrics | 6,31,44,45 |
| [2002 核对单组件 Tracker 在关闭时保留已执行计量](../done/2002-tracker-direct-close-before-publication.md) | P2 | 补证 | x/observability/ops | 22,26,28,42,44,47 |
| [2003 核对外部 Manager 的直接检查仍使用兼容回退](../done/2003-direct-tracker-external-manager-fallback.md) | P2 | 补证 | x/observability/ops | 6,11,31,42,44 |
| [1957 健康 Manager 的 Enabled 字段缺少兼容语义说明](../done/1957-health-enabled-compatibility-doc.md) | P3 | 已复现 | x/observability/ops | 6,14,16,42,52 |
| [1961 核对 Use 中 nil 槽位的处理与冻结边界](../done/1961-use-nil-positions-continue-after-freeze.md) | P3 | 补证 | core | 5,28,36,46 |
| [1964 锁定路由清单相同模式下的方法顺序](../done/1964-print-method-order-same-pattern.md) | P3 | 补证 | router | 14,32,37,52 |
| [1965 核对反向路由参数不成对时的诊断](../done/1965-reverse-url-unpaired-param-interaction.md) | P3 | 补证 | router | 12,15,37,46 |
| [1968 核对 RouteState 对仅大小写不同参数键的快照](../done/1968-route-state-param-case-order.md) | P3 | 补证 | contract | 12,25,31,38 |
| [1969 核对有效无采样 TraceContext 的往返](../done/1969-trace-sampling-empty-propagation.md) | P3 | 补证 | contract | 25,27,31,38 |
| [1973 核对 nil tracer 时请求和 writer 原样交付](../done/1973-tracing-nil-tracer-handler-identity.md) | P3 | 补证 | middleware/tracing | 5,8,25,39 |
| [1974 核对 nil Observer 时不会提前包裹 writer](../done/1974-httpmetrics-nil-observer-handler-identity.md) | P3 | 补证 | middleware/httpmetrics | 5,8,29,39 |
| [1975 核对请求标识固定宽度字段的往返](../done/1975-requestid-decode-padded-fields.md) | P3 | 补证 | middleware/requestid | 6,25,39,45 |
| [1979 核对 CSP sandbox 在空标记与值之间的切换](../done/1979-csp-sandbox-flag-value-roundtrip.md) | P3 | 补证 | security/headers | 6,16,40,45 |
| [1980 核对 Principal 的 nil 与空属性快照](../done/1980-principal-nil-versus-empty-attributes.md) | P3 | 补证 | security/authn | 25,31,40,45 |
| [1981 核对普通输入的密码长度计量单位](../done/1981-password-length-unit-contract.md) | P3 | 补证 | security/password | 6,15,40,46 |
| [1983 核对编译后的 HSTS 策略在普通 HTTP 与 TLS 间独立](../done/1983-hsts-policy-compiled-per-request-selection.md) | P3 | 补证 | security/headers | 6,16,31,40 |
| [1989 核对 SQL 连接池上限收缩后的配置](../done/1989-db-apply-open-idle-pool-shrink.md) | P3 | 补证 | store/db | 14,28,34,41 |
| [1991 核对幂等状态枚举的精确值边界](../done/1991-idempotency-status-valid-exact-enum.md) | P3 | 补证 | store/idempotency | 6,16,34,41 |
| [1992 核对健康详情中显式 null 和空详情的 JSON 差异](../done/1992-details-null-value-versus-omitted.md) | P3 | 补证 | health | 6,16,31,42 |
| [1993 核对组件零耗时的 JSON 往返](../done/1993-component-zero-duration-json-roundtrip.md) | P3 | 补证 | health | 6,16,42,45 |
| [1994 核对 Ready 与空原因的序列化组合](../done/1994-readiness-reason-zero-value-shape.md) | P3 | 补证 | health | 6,16,42,45 |
| [1997 核对 JSON 日志中合法非 ASCII 字段名的稳定保留](../done/1997-json-unicode-name-order.md) | P3 | 补证 | log | 16,29,31,43 |
| [1998 核对基础指标在合成计数上限的行为](../done/1998-base-collector-counter-capacity-contract.md) | P3 | 补证 | metrics | 6,34,44,46 |
| [2000 核对多收集器 Clear 与 Record 交错的子顺序](../done/2000-multi-clear-interleaved-child-order.md) | P3 | 补证 | metrics | 28,31,44,47 |
| [2001 核对根门面 Param 在 Prepare 后的路径上下文](../done/2001-facade-param-through-prepared-nested-group.md) | P3 | 补证 | 根门面 | 3,8,25,36,37 |
| [2004 核对报告中注册未尝试与无组件状态](../done/2004-health-report-empty-registered-component.md) | P3 | 补证 | x/observability/ops | 6,16,31,42 |
| [2005 测量小页历史 CSV 的过滤与编码成本](../done/2005-history-csv-sparse-filter-cost.md) | P3 | 补证 | x/observability/ops | 29,35,42,48 |
| [2006 核对匹配缓存预热前后的 Allow 响应头所有权](../done/2006-method-allow-clone-across-cached-requests.md) | P3 | 补证 | router | 13,23,31,32,37,47 |
| [2007 核对已准备 Server 的请求保留调用方 context](../done/2007-server-prepared-request-context-parent.md) | P3 | 补证 | core | 5,22,25,36 |
