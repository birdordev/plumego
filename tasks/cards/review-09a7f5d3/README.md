# 稳定模块质量复审实施记录

本批含 **58 张可独立执行的任务卡**，编号 1632–1689。依据为 [稳定模块第 9 节复审](../../../docs/evidence/stable-quality-review-09a7f5d3.md)。上一轮完成卡与既有待原生平台验证的 1615 保留原记录。

58 张任务卡均已执行并归档。1632 保持同步日志写入语义，输出回调通过独立 sink 记录诊断。1633 保持已选状态先于 Flush 发送，标准接口无法预判运行时拒绝。这两项均补齐约束和回归。1642 保留可能重叠的 TLS 读取，并记录并发成本与单一发布证据。

| 卡片 | 优先级 | 证据 | 主模块 | 检查号 |
|---|---|---|---|---|
| [1632 JSON 日志写入器重入时锁住调用方](../done/1632-sink-reentry.md) | P1 | 已复现 | log | 29,32,33,43,47 |
| [1633 HEAD 被拒绝的 Flush 提前结束长度推断](../done/1633-head-selected-flush-refusal.md) | P2 | 已复现 | router | 6,23,24,37 |
| [1634 独立 Chain 的 nil builder 在 Build 时 panic](../done/1634-standalone-chain-nil.md) | P2 | 已复现 | middleware | 6,15,20,45 |
| [1635 附加请求 ID 时原请求头与衍生请求共用写入](../done/1635-requestid-header-ownership.md) | P2 | 已复现 | middleware | 6,16,27,34 |
| [1636 Additional 的规范化同名项导致输出取决于 map 顺序](../done/1636-policy-header-name-conflict.md) | P2 | 源码确认 | security | 14,32,34,40,46 |
| [1637 tracer 返回的 SpanID 在输出前没有统一验证](../done/1637-trace-outbound-id-validation.md) | P2 | 源码确认 | middleware | 18,25,27,39 |
| [1638 显式正文预览直接复制应用错误正文](../done/1638-debug-preview-policy.md) | P2 | 源码确认 | middleware | 21,25,35,39 |
| [1639 缓存的 payload 配额无法约束空值条目数量](../done/1639-cache-empty-payload-budget.md) | P2 | 源码确认 | store | 14,29,35,41,48 |
| [1640 数据库构造错误直接格式化注入 opener 的错误文本](../done/1640-db-open-safe-diagnostics.md) | P2 | 源码确认 | store | 22,25,30,41 |
| [1641 手机号字数校验分配完整 rune 切片](../done/1641-phone-digit-count-cost.md) | P2 | 已复现 | security | 9,12,35,48 |
| [1642 并发 Prepare 单次发布但可重复读 TLS 文件](../done/1642-prepare-tls-work-ownership.md) | P2 | 源码确认 | core | 14,28,31,36,48 |
| [1645 验证中间件构建异常退出释放资格](../done/1645-builder-goexit-retry.md) | P2 | 补证 | core | 28,29,33,36,46 |
| [1646 验证连接接管和关停计数](../done/1646-shutdown-hijack-accounting.md) | P2 | 补证 | core | 28,29,32,36 |
| [1647 验证构造失败与路由选项快照交错](../done/1647-route-options-failed-publish.md) | P2 | 补证 | core | 19,28,31,36,47 |
| [1648 验证 Ready 回调 panic 后组可继续 Stop](../done/1648-ready-panic-state.md) | P2 | 补证 | core | 28,29,33,36 |
| [1650 补证通配 URL 对百分号及 Unicode 的往返](../done/1650-wildcard-url-escaping.md) | P2 | 补证 | router | 18,34,37,45 |
| [1651 补证 Group 前缀异常和命名表原子性](../done/1651-group-registration-errors.md) | P2 | 补证 | router | 15,19,31,37 |
| [1652 度量冻结路由的 Allow 查询成本](../done/1652-frozen-method-negotiation-load.md) | P2 | 补证 | router | 24,35,37,48 |
| [1653 记录顺序文件复制中断的最终状态](../done/1653-static-store-reader-errors.md) | P2 | 补证 | router | 22,23,24,29,37 |
| [1654 核对 HEAD 和 Trailer 声明的组合行为](../done/1654-head-trailers-transport-order.md) | P2 | 补证 | router | 23,24,37 |
| [1655 测量追踪 baggage 的复制与大字段成本](../done/1655-trace-baggage-large-copy.md) | P2 | 补证 | contract | 27,34,35,48 |
| [1656 核对请求 ID 的多字节长度与响应回显](../done/1656-request-id-byte-boundary.md) | P2 | 补证 | contract | 18,25,27,38 |
| [1657 补证清理无正文帧信息时保留业务头](../done/1657-json-bodyless-preserve-headers.md) | P2 | 补证 | contract | 6,23,24,38 |
| [1658 核对 AsAPIError 包装链中的 typed nil](../done/1658-typed-nil-apierror-chain.md) | P2 | 补证 | contract | 21,22,38,46 |
| [1659 核对认证器返回新请求时的上下文继承](../done/1659-auth-enriched-context-contract.md) | P2 | 补证 | middleware | 20,26,27,39,40 |
| [1660 验证正文包装遇读取错误和 Close 的所有权](../done/1660-bodylimit-read-error-close.md) | P2 | 补证 | middleware | 20,26,29,39,46 |
| [1661 核对 SSE 媒体类型解析与权重契约](../done/1661-compression-negotiation-casing.md) | P2 | 补证 | middleware | 6,18,24,39 |
| [1662 验证 Vary writer 在不支持 Flush 后的状态回滚](../done/1662-vary-flush-retry-ownership.md) | P2 | 补证 | middleware | 23,24,34,39 |
| [1663 验证 logger 失败不会改变已提交结果](../done/1663-accesslog-finalizer-panic.md) | P2 | 补证 | middleware | 23,25,33,39 |
| [1664 核对合并回放中外层多值头的所有权](../done/1664-singleflight-header-delta-labels.md) | P2 | 补证 | middleware | 24,32,34,39 |
| [1665 补证禁用、中等和最大容量的配置约束](../done/1665-concurrency-config-boundary.md) | P2 | 补证 | middleware | 14,15,29,35 |
| [1666 验证调试路径元数据的最小公开范围](../done/1666-debug-request-path-privacy.md) | P2 | 补证 | middleware | 18,25,27,39 |
| [1667 核对超时替换对外层头部所有权的影响](../done/1667-timeout-inherited-response-fields.md) | P2 | 补证 | middleware | 21,23,24,39 |
| [1668 核对 JWT 时钟和宽限窗口边界](../done/1668-jwt-verification-clock-boundary.md) | P2 | 补证 | security | 6,14,40,46 |
| [1669 复核候选签名密钥持久化的只读快照约定](../done/1669-jwt-candidate-store-snapshot.md) | P2 | 补证 | security | 26,30,31,34,40 |
| [1670 测量编译后安全头每次重复校验成本](../done/1670-headers-compiled-validation-cost.md) | P2 | 补证 | security | 14,16,40,48 |
| [1671 验证 limiter Reset 与 RetryAfter 的极值语义](../done/1671-limiter-allow-policy-timestamps.md) | P2 | 补证 | security | 14,32,35,40 |
| [1672 补证规范邮件地址本地部分和域标签的边界](../done/1672-input-email-length-compatibility.md) | P2 | 补证 | security | 6,14,18,40 |
| [1673 确认目录同步与目录关闭错误的职责](../done/1673-kv-snapshot-postrename-errors.md) | P2 | 补证 | store | 22,29,30,31,41 |
| [1674 核对 GetStats 同次读到的命中与未命中总计](../done/1674-kv-stats-concurrent-observation.md) | P2 | 补证 | store | 31,32,34,41 |
| [1675 明确过期键与 Delete 的存在语义](../done/1675-kv-key-expiry-delete.md) | P2 | 补证 | store | 6,16,31,41 |
| [1676 核对清除过期项后写入拒绝的可观察状态](../done/1676-cache-expired-append-cleanup.md) | P2 | 补证 | store | 28,30,31,35,41 |
| [1677 验证扫描回调异常后结果集关闭](../done/1677-db-rows-callback-abnormal-exit.md) | P2 | 补证 | store | 22,29,30,41 |
| [1678 验证 Clear 与已取得值副本的隔离](../done/1678-cache-clear-reader-ownership.md) | P2 | 补证 | store | 28,32,34,41 |
| [1679 厘清 File Error.Error 的路径和底层文案契约](../done/1679-file-error-classification.md) | P2 | 补证 | store | 16,22,25,41 |
| [1680 核对不可序列化详情对探测输出的影响](../done/1680-health-detail-json-failure.md) | P2 | 补证 | x/observability | 21,23,34,42,46 |
| [1681 补证健康检查取消时的宿主缓存粒度](../done/1681-health-check-context-contract.md) | P2 | 补证 | x/observability | 26,32,42,46 |
| [1683 核对 WithFields 保留嵌套数据的复制约定](../done/1683-structured-field-nested-ownership.md) | P2 | 补证 | log | 16,32,34,43 |
| [1684 验证过滤级别和 discard 模式的 FatalCtx 语义](../done/1684-fatal-matrix-exit-status.md) | P2 | 补证 | log | 6,29,43,46 |
| [1685 验证文本内部文件 Flush 与 Close 的锁序](../done/1685-file-flush-close-overlap.md) | P2 | 补证 | log | 28,29,30,32,43 |
| [1686 核对聚合器处理失约子统计的语义](../done/1686-aggregation-invalid-child-stats.md) | P2 | 补证 | metrics | 14,22,32,44 |
| [1687 验证 HTTP record 的 labels 可变所有权与单位](../done/1687-http-record-dimensions.md) | P2 | 补证 | metrics | 16,34,44,45 |
| [1688 核对 ObserveHTTP 的 fan-out 取消传播](../done/1688-multi-observe-context.md) | P2 | 补证 | metrics | 26,33,44,47 |
| [1643 KV 主文档仍推荐已标记废弃的配置入口](../done/1643-kv-canonical-config-primer.md) | P3 | 源码确认 | store | 6,7,16,52 |
| [1644 日志构造器只使用首个 variadic 配置](../done/1644-logger-config-arity-doc.md) | P3 | 源码确认 | log | 6,14,16 |
| [1649 验证 drain 间隔零值与负值含义](../done/1649-configuration-drain-interval.md) | P3 | 补证 | core | 6,14,16,36 |
| [1682 明确模型 Duration 的负值线格式](../done/1682-health-negative-duration.md) | P3 | 补证 | health | 6,16,42 |
| [1689 验证根门面 Param 示例与命名路由的一致性](../done/1689-facade-param-public-example.md) | P3 | 补证 | 根门面 | 3,6,19,37 |


## 验证

- 稳定与受影响扩展的定向 race 回归通过。
- `make lint` 使用仓库固定的 v2.14.0，结果为 0 项。
- `make stable-coverage` 实测 96.4%。
- `make website-sync` 与 `make website-check-generated` 通过，生成文件无差异。
- `make validate-diff` 通过，选中 `cross_module_public_api_release` 并执行完整 Go 门禁。
- 58 张归档卡通过结构检查，395 处引用有效。五项重点并发与生命周期回归用 race 选项重复八次通过。

## 本机基准

Go 1.26.0、darwin/arm64、Apple M1 Pro。下表记三次采样中位数，除电话样例的分配前后对照外，仅用于记录当前成本。

```sh
go test -p 1 -run '^$' -bench 'Benchmark(ConcurrentTLSPrepare|FrozenAllowLookup|TraceBaggageCopies|CompiledHeaderValidation|ValidatePhoneDigitCount|MemoryCacheEmptyEntries)$' -benchmem -benchtime=150ms -count=3 ./core ./router ./contract ./security/headers ./security/input ./store/cache
```

| 场景 | ns/op | B/op | allocs/op |
|---|---:|---:|---:|
| ConcurrentTLSPrepare/1 | 569776.00 | 26450 | 158 |
| ConcurrentTLSPrepare/4 | 723280.00 | 86202 | 560 |
| FrozenAllowLookup/2 | 1448.00 | 1330 | 15 |
| FrozenAllowLookup/16 | 2529.00 | 1692 | 15 |
| FrozenAllowLookup/64 | 7481.00 | 3071 | 15 |
| TraceBaggageCopies/2 | 295.80 | 784 | 6 |
| TraceBaggageCopies/64 | 1565.00 | 10016 | 10 |
| CompiledHeaderValidation/0 | 505.90 | 80 | 5 |
| CompiledHeaderValidation/32 | 5590.00 | 2424 | 40 |
| ValidatePhoneDigitCount/1234567 | 15.75 | 0 | 0 |
| ValidatePhoneDigitCount/1234567_(1234567) | 28.97 | 0 | 0 |
| MemoryCacheEmptyEntries/1 | 706.80 | 833 | 11 |
| MemoryCacheEmptyEntries/1000 | 230704.00 | 170142 | 3366 |
