# 稳定模块第 9 节复审实施索引

基线为 `2f962d21`。编号 2064–2118，共 **55 张**。3 张处理已复现的行为问题，3 张处理源码确认的文档缺口，49 张用于定向补证。 [复审报告](../../../docs/evidence/stable-quality-review-2f962d21.md)

55 张任务卡已完成并归档。包含 3 项行为修复、3 项契约说明、48 张定向补证和 1 张分析卡。

| 卡片 | 优先级 | 证据 | 主模块 | 第 9 节检查号 | 依赖 |
|---|---|---|---|---|---|
| [2064 批次发布在管理器锁内调用上下文方法](../done/2064-aggregate-context-callback-outside-manager-lock.md) | P1 | 已复现 | `x/observability/ops` | 26,32,33,42,45,47 | 无 |
| [2065 归档修改时间并列时保留较早世代](../done/2065-retention-tied-mtime-keeps-newer-generation.md) | P2 | 已复现 | `log` | 14,29,35,43,45 | 无 |
| [2066 健康报告可能同时列出两个注册世代](../done/2066-report-built-in-manager-coherent-snapshot.md) | P2 | 已复现 | `x/observability/ops` | 16,32,34,42,45 | 无 |
| [2070 部分启动回滚后注册保持冻结](../done/2070-lifecycle-partial-start-keeps-registration-frozen.md) | P2 | 补证 | `core` | 28,30,36,46 | 无 |
| [2071 装配回调 panic 后重试取得所有权](../done/2071-prepare-retry-after-builder-panic-owner.md) | P2 | 补证 | `core` | 23,28,33,36,47 | 无 |
| [2073 排空诊断只计连接而非请求轮次](../done/2073-connection-drain-ignores-active-idle-transitions.md) | P2 | 补证 | `core` | 28,29,32,36,47 | 无 |
| [2074 冻结前后 nil option 与元数据快照](../done/2074-frozen-route-nil-options-visibility.md) | P2 | 补证 | `router` | 6,19,33,37 | 无 |
| [2075 普通无扩展名静态内容的 MIME 与 HEAD](../done/2075-static-extensionless-mime-head-parity.md) | P2 | 补证 | `router` | 17,24,37,45 | 无 |
| [2076 HEAD 信息响应后拒绝 Flush 的终态](../done/2076-head-flush-error-after-informational-wrapper.md) | P2 | 补证 | `router` | 23,24,37,46 | 无 |
| [2078 新增显式方法使已缓存 Any 匹配失效](../done/2078-route-registration-replaces-cached-any-match.md) | P2 | 补证 | `router` | 19,31,32,37,47 | 无 |
| [2079 同一请求重试编码保持相关 ID](../done/2079-write-response-context-after-error-request-id.md) | P2 | 补证 | `contract` | 21,23,27,38,46 | 无 |
| [2080 错误详情中具名字节切片的快照](../done/2080-api-error-detail-typed-byte-slices-snapshot.md) | P2 | 补证 | `contract` | 22,34,38,45 | 无 |
| [2082 无正文状态对现有响应头的提交边界](../done/2082-bodyless-write-declined-no-marshal.md) | P2 | 补证 | `contract` | 21,23,24,38 | 无 |
| [2083 超时后迟到 Flush 和正文隔离](../done/2083-timeout-canceled-buffer-following-flush-unavailable.md) | P2 | 补证 | `middleware/timeout` | 23,24,26,39,47 | 无 |
| [2084 压缩 103 后的 304 保留响应协议](../done/2084-gzip-wrapper-information-then-bodyless-terminal.md) | P2 | 补证 | `middleware/compression` | 23,24,39,45 | 无 |
| [2085 合流 204 的空体重放与下一批隔离](../done/2085-shared-replay-no-body-204-with-adjacent-flight.md) | P2 | 补证 | `middleware/singleflight` | 23,24,31,39,47 | 无 |
| [2087 缺预检方法的 OPTIONS 保留应用处理](../done/2087-cors-options-without-preflight-method-passes-through.md) | P2 | 补证 | `middleware/cors` | 18,20,39,46 | 无 |
| [2088 访问日志的部分写入字节观察](../done/2088-logging-and-metrics-finalization-on-short-http-write.md) | P2 | 补证 | `middleware/accesslog` | 23,24,25,39,45 | 无 |
| [2089 恢复层区分信息状态与最终错误](../done/2089-recovery-panic-after-informational-response.md) | P2 | 补证 | `middleware/recovery` | 21,23,24,39,46 | 无 |
| [2091 BodyLimit 精确字节并带 EOF 的输入](../done/2091-bodylimit-exact-read-with-eof-preserves-success.md) | P2 | 补证 | `middleware/bodylimit` | 20,24,35,39,46 | 无 |
| [2092 密钥存储 Keys 快照简单并发](../done/2092-keystore-key-snapshot-order-and-cancel.md) | P2 | 补证 | `security/jwt` | 26,32,34,40,47 | 无 |
| [2093 编译策略的 HSTS 零值与正常 TLS](../done/2093-hsts-compile-zero-option-cross-request.md) | P2 | 补证 | `security/headers` | 14,20,40,46 | 无 |
| [2095 限流过期清理与停止的统计顺序](../done/2095-limiter-snapshot-after-entry-expiry-and-stop.md) | P2 | 补证 | `security/abuse` | 28,32,35,40,47 | 无 |
| [2097 小容量重开后的纯读和下一次删除](../done/2097-kv-open-downsize-does-not-rewrite-until-delete.md) | P2 | 补证 | `store/kv` | 28,31,35,41,46 | 无 |
| [2098 KV 过期读统计与文件所有权](../done/2098-kv-stats-expiry-preserves-counters-across-reopen.md) | P2 | 补证 | `store/kv` | 28,31,32,41,46 | 无 |
| [2099 快照注入短写返回值的假设](../done/2099-kv-snapshot-short-write-injected-contract.md) | P2 | 补证 | `store/kv` | 22,29,30,31,41 | 无 |
| [2100 已有显式空值追加空输入保持形状](../done/2100-cache-active-empty-append-retains-non-nil-shape.md) | P2 | 补证 | `store/cache` | 6,31,34,41,45 | 2067 |
| [2101 非整数 Incr failure 后容量可复用](../done/2101-cache-noninteger-value-keeps-expiry-and-capacity.md) | P2 | 补证 | `store/cache` | 22,31,35,41,46 | 无 |
| [2102 SQL 扫描回调异常退出释放结果集](../done/2102-sql-pool-rows-closed-on-scan-callback-goexit.md) | P2 | 补证 | `store/db` | 29,30,31,41,47 | 无 |
| [2108 同时间归档清理仍保护活跃链接与多级别](../done/2108-retention-tie-keeps-live-link-protected.md) | P2 | 补证 | `log` | 28,29,35,43,45 | 2065 |
| [2110 文本自定义格式失败后的界线](../done/2110-text-formatter-write-failure-retains-delimiters.md) | P2 | 补证 | `log` | 25,33,43,46 | 无 |
| [2111 聚合子收集器重置形成的新起点](../done/2111-multi-zero-later-timestamps-retain-earliest-on-clear.md) | P2 | 补证 | `metrics` | 28,32,44,47 | 无 |
| [2112 空名称记录在总量上界的计量](../done/2112-base-empty-name-and-explicit-error-at-capacity.md) | P2 | 补证 | `metrics` | 32,35,44,45 | 无 |
| [2115 健康批次进入提交前取消仍按类别返回](../done/2115-aggregate-cancel-between-admission-and-publish.md) | P2 | 补证 | `x/observability/ops` | 26,31,33,42,47 | 2064 |
| [2116 报告里的手动就绪与组件健康独立](../done/2116-report-health-readiness-publication-order.md) | P2 | 补证 | `x/observability/ops` | 28,32,42,47 | 2066 |
| [2117 历史导出过末页的 JSON 与 CSV 形状](../done/2117-history-export-offset-beyond-end-content-types.md) | P2 | 补证 | `x/observability/ops` | 6,21,24,38,42 | 无 |
| [2067 说明空切片 Append 的归一化](../done/2067-describe-cache-empty-append-normalization.md) | P3 | 源码确认 | `store/cache` | 6,14,16,34,41 | 无 |
| [2068 明确事务回调的提交与回滚所有权](../done/2068-document-withtransaction-finalization-ownership.md) | P3 | 源码确认 | `store/db` | 16,22,29,31,41 | 无 |
| [2069 说明关闭后的批次历史与尝试计量](../done/2069-document-tracker-closed-history-accounting.md) | P3 | 源码确认 | `x/observability/ops` | 16,28,31,42,52 | 无 |
| [2072 App.Any 的命名与标签贯通](../done/2072-any-metadata-roundtrip-through-core.md) | P3 | 补证 | `core` | 6,19,34,36 | 无 |
| [2077 通配反查编码普通百分号与非 ASCII](../done/2077-reverse-named-wildcard-unicode-escaped-percent.md) | P3 | 补证 | `router` | 6,18,37,45 | 无 |
| [2081 nil 与已有请求 ID 的访问器语义](../done/2081-request-id-nil-context-and-existing-value.md) | P3 | 补证 | `contract` | 16,27,38,45 | 无 |
| [2086 合法已有相关 ID 时跳过生成回调](../done/2086-requestid-explicit-header-skips-generator.md) | P3 | 补证 | `middleware/requestid` | 18,20,27,39 | 无 |
| [2090 Chain 快照顺序与原切片隔离](../done/2090-chain-snapshot-mutation-and-input-tail.md) | P3 | 补证 | `middleware` | 5,20,34,39 | 无 |
| [2094 密码强度配置零值的公开行为](../done/2094-password-strength-zero-explicit-categories.md) | P3 | 补证 | `security/password` | 6,14,40,46 | 无 |
| [2096 请求上下文替换 Principal 时父快照隔离](../done/2096-principal-overwrite-value-shape-isolation.md) | P3 | 补证 | `security/authn` | 27,34,40,45 | 无 |
| [2103 文件元数据类型的可选零值与所有权](../done/2103-file-copy-contract-output-defaults.md) | P3 | 补证 | `store/file` | 16,34,41,45 | 无 |
| [2104 幂等记录副本的时间字段和过期判定](../done/2104-idempotency-clone-expiry-judgment-is-independent.md) | P3 | 补证 | `store/idempotency` | 16,34,41,45 | 无 |
| [2105 就绪模型 Clone 的显式空 map 独立性](../done/2105-readiness-clone-preserves-explicit-empty-map.md) | P3 | 补证 | `health` | 6,16,34,42 | 无 |
| [2106 健康 Details 的具名容器浅复制](../done/2106-clone-keeps-detail-defined-map-values-shallow.md) | P3 | 补证 | `health` | 16,34,42,45 | 无 |
| [2107 未知状态值和依赖 JSON 往返](../done/2107-health-status-unknown-wire-roundtrip-with-dependencies.md) | P3 | 补证 | `health` | 6,16,38,42 | 无 |
| [2109 JSON 具名无符号整数与空指针混合字段](../done/2109-json-nil-typed-interface-field-precision.md) | P3 | 补证 | `log` | 6,34,43,45 | 无 |
| [2113 HTTP 记录经多子分发的快照链](../done/2113-http-record-label-order-isolated-after-fanout.md) | P3 | 补证 | `metrics` | 5,34,44,45 | 无 |
| [2114 门面应用关闭后的直接路径参数](../done/2114-facade-after-shutdown-keeps-route-param-context.md) | P3 | 补证 | 根门面 | 3,6,27,36 | 无 |
| [2118 报告组件名切片的调用方修改隔离](../done/2118-report-updates-own-component-name-slice.md) | P3 | 补证 | `x/observability/ops` | 34,42,45 | 2066 |
