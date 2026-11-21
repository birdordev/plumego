# 稳定模块质量复审实施记录

本批含 **53 张可独立执行的任务卡**，编号 1690–1742。依据为 [稳定模块第 9 节复审](../../../docs/evidence/stable-quality-review-94305741.md)。上一轮完成卡与既有待原生平台验证的 1615 保留原记录。

已实施并归档 1690–1742。健康宿主、正文限制和关停的公开行为变更已同步到对应模块说明。注入时钟限流器的清理改为按请求推进，保持虚拟时间域；默认时钟继续定时清理。KV 的预算归因在内部错误链保留，公开分类留给单独的 API 审核。

| 卡片 | 优先级 | 审查时证据 | 主模块 | 检查号 |
|---|---|---|---|---|
| [1690 未知健康状态在摘要入口映射为成功](../done/1690-health-unknown-status.md) | P1 | 已复现 | x/observability | 6,21,38,42 |
| [1691 全量组件端点在编码失败时返回空 200](../done/1691-components-encode-failure.md) | P1 | 已复现 | x/observability | 21,22,23,38,42,46 |
| [1692 清理时钟回调重入 Stop 导致互等](../done/1692-limiter-clock-stop-reentry.md) | P1 | 已复现 | security | 28,29,33,40,47 |
| [1693 正文超限后被舍弃的写入返回成功](../done/1693-bodylimit-write-result.md) | P2 | 已复现 | middleware | 6,20,22,23,39 |
| [1694 正文限制覆写传入请求的 Body](../done/1694-body-replacement-request-ownership.md) | P2 | 源码确认 | middleware | 6,18,26,29,34 |
| [1695 回放超限后缓冲容量仍被保留](../done/1695-timeout-buffer-reset-retention.md) | P2 | 源码确认 | middleware | 16,23,29,35,48 |
| [1696 关停诊断回调可覆盖 Shutdown 错误](../done/1696-shutdown-diagnostic-panic.md) | P2 | 源码确认 | core | 22,28,33,36 |
| [1697 KV 读取预算超出缺少机器可判结果](../done/1697-kv-limit-error-classification.md) | P2 | 源码确认 | store | 14,22,29,41 |
| [1698 合并器成功回放未扣减等待者计数](../done/1698-coalescer-waiter-lifetime-accounting.md) | P2 | 源码确认 | middleware | 28,29,32,35 |
| [1699 内部日志轮换失败时可能丢失原活动链接](../done/1699-rotation-active-link-publication.md) | P2 | 源码确认 | log | 29,30,31,43 |
| [1702 核对注入 typed nil logger 的构造约定](../done/1702-logger-typed-nil-startup.md) | P2 | 补证 | core | 5,14,15,36 |
| [1703 验证 Run 监听失败后服务器句柄的复用](../done/1703-run-bind-failure-server-identity.md) | P2 | 补证 | core | 6,28,31,36 |
| [1704 核对嵌套 LifecycleGroup 回滚后的保留项](../done/1704-lifecycle-nested-group-rollback.md) | P2 | 补证 | core | 28,29,30,36,46 |
| [1705 核对 core 包装与 router 注册错误的分类稳定性](../done/1705-route-error-format-context.md) | P2 | 补证 | core | 19,22,36 |
| [1706 验证排水计数正常时终止恢复行为](../done/1706-shutdown-context-observation.md) | P2 | 补证 | core | 26,29,32,36 |
| [1707 锁定冻结对已有 group 视图的共享作用](../done/1707-group-freeze-shared-views.md) | P2 | 补证 | router | 28,31,32,37 |
| [1708 测量深路径在对象池回收时的内存保留](../done/1708-path-pool-retention-limit.md) | P2 | 补证 | router | 29,34,35,48 |
| [1709 核对静态空文件与显式媒体类型](../done/1709-static-empty-seekable-length.md) | P2 | 补证 | router | 6,24,37 |
| [1710 覆盖 Allow 对路径归一化与冻结结果的一致性](../done/1710-allow-no-trailing-variants.md) | P2 | 补证 | router | 18,32,37,45 |
| [1712 核对错误链多个分支的遍历优先级](../done/1712-error-chain-shared-branches.md) | P2 | 补证 | contract | 6,22,38 |
| [1713 核对 JSON 池在慢写与早退时的归属](../done/1713-pooled-buffers-concurrent-ownership.md) | P2 | 补证 | contract | 23,29,32,34,38 |
| [1714 核对 request ID 与 RouteState 共存时的快照](../done/1714-request-context-nested-carriers.md) | P2 | 补证 | contract | 27,34,38 |
| [1715 补证错误 details 整数与空值的线格式](../done/1715-writeerror-default-json-numeric-details.md) | P2 | 补证 | contract | 6,21,38,45 |
| [1716 验证已选 CORS 响应头与下游替换的所有权](../done/1716-cors-vary-final-header-owner.md) | P2 | 补证 | middleware | 20,23,24,39 |
| [1717 核对恢复层在 transport Flush 失败后的终态权](../done/1717-recovery-early-flush-terminal.md) | P2 | 补证 | middleware | 23,24,39,46 |
| [1718 核对 gzip 对媒体编码和摘要头的取舍](../done/1718-gzip-file-content-encoding-policy.md) | P2 | 补证 | middleware | 6,23,24,39 |
| [1719 补证随机池回绕下的生成吞吐](../done/1719-requestid-allocator-concurrency.md) | P2 | 补证 | middleware | 32,35,39,48 |
| [1720 补证本地期限与上游取消的接近判决](../done/1720-timeout-local-vs-parent-cancel.md) | P2 | 补证 | middleware | 23,26,32,39,47 |
| [1721 核对收尾 observe 对自定义回调的阻塞边界](../done/1721-observer-panic-timing.md) | P2 | 补证 | middleware | 29,33,39,47 |
| [1722 验证注入 limiter 生命周期和后续使用](../done/1722-abuseguard-injected-ownership-after-stop.md) | P2 | 补证 | middleware | 5,28,29,39,40 |
| [1723 核对 limiter 独立统计字段与稳定状态](../done/1723-limiter-metric-snapshot-policy.md) | P2 | 补证 | security | 32,34,40,47 |
| [1724 核对静态认证凭据来源说明](../done/1724-authn-header-sources.md) | P2 | 补证 | security | 18,25,40,46 |
| [1725 核对直接 TLS 与可信代理约定](../done/1725-headers-hsts-tls-provenance.md) | P2 | 补证 | security | 14,18,40,46 |
| [1726 补证管理器持有配置的复制及默认值](../done/1726-jwt-builder-config-isolation.md) | P2 | 补证 | security | 5,14,34,40 |
| [1727 补证 Header 值接受集和诊断输出](../done/1727-input-header-unicode-domain.md) | P2 | 补证 | security | 18,25,40,46 |
| [1728 确认读过期键后内存与持久目录不变](../done/1728-kv-read-immutable-expiry.md) | P2 | 补证 | store | 28,31,34,41 |
| [1729 补证历史快照加载后 Get 副本生命周期](../done/1729-kv-lookup-return-isolation.md) | P2 | 补证 | store | 29,32,34,41 |
| [1730 核对后台清理与覆盖时的待清理判据](../done/1730-cache-expiry-cleanup-counter.md) | P2 | 补证 | store | 28,31,32,41,47 |
| [1731 验证旧整数格式更新后的持久迁移外观](../done/1731-cache-legacy-integer-migration.md) | P2 | 补证 | store | 6,22,31,41 |
| [1732 补证数据库连接池重配置的顺序和零值](../done/1732-db-apply-config-order.md) | P2 | 补证 | store | 14,29,32,41 |
| [1733 补证 File.Clone 的时间指针和元数据并行归属](../done/1733-file-clone-timestamp-ownership.md) | P2 | 补证 | store | 16,34,41 |
| [1734 验证组件启用标志与健康状态的序列化独立性](../done/1734-component-enabled-wire-independence.md) | P2 | 补证 | health | 6,16,38,42 |
| [1735 核对就绪快照时间和值语义](../done/1735-readiness-time-source-clone.md) | P2 | 补证 | health | 6,16,34,42 |
| [1736 测量字段拒绝与多层截断的边界](../done/1736-logger-invalid-field-offsets.md) | P2 | 补证 | log | 25,35,43,48 |
| [1737 核对内部轮换丢失旧句柄关闭错误的影响](../done/1737-text-rotation-writer-close-diagnostic.md) | P2 | 补证 | log | 22,29,30,43 |
| [1739 核对单目标 fast path 的字段所有权](../done/1739-multi-singleton-delegation.md) | P2 | 补证 | metrics | 6,13,34,44 |
| [1740 验证空名称记录与统计占用的关系](../done/1740-collector-unnamed-count-invariant.md) | P2 | 补证 | metrics | 6,14,32,44 |
| [1741 度量 collector 构造时指针过滤与身份判断](../done/1741-aggregate-pointer-nil-cost.md) | P2 | 补证 | metrics | 5,12,44,48 |
| [1700 观测回调注释将路由覆盖写成整个下游请求](../done/1700-telemetry-observer-context-comment.md) | P3 | 源码确认 | middleware | 16,27,34,39 |
| [1701 DB 打开文档缺少连接池延迟建连约定](../done/1701-database-open-doc-laziness.md) | P3 | 源码确认 | store | 6,15,16,41 |
| [1711 补证 Print 首个写错误后停止输出](../done/1711-print-writer-failure-contract.md) | P3 | 补证 | router | 16,22,29,37 |
| [1738 核对 JSON 自定义字段失败与完整写入组合](../done/1738-json-fields-level-panic-recovery.md) | P3 | 补证 | log | 23,33,43,46 |
| [1742 核对根门面的依赖别名和默认日志](../done/1742-facade-logger-ownership-example.md) | P3 | 补证 | 根门面 | 3,5,6,16 |


## 验证记录

卡片中的历史行号固定在审查基线，实施证据链接指向当前测试。

| 命令 | 结果 |
|---|---|
| `make fmt`、`make fmt-check` | 通过。 |
| `make validate-diff-dry`、`make validate-diff` | 通过。跨模块配置执行 `make gates`，包含根模块 race、vet、边界、公开 API 快照、文档示例、参考程序和 CLI。 |
| `make test` | 首轮达到 20 秒上限时，未改动的 `x/messaging/pubsub` 正在运行 `TestPublishDelayed_Basic`。重跑通过，pubsub 用时 17.992 秒。 |
| `make stable-coverage` | 通过，稳定模块语句覆盖率 96.6%。 |
| `make lint` | 通过，golangci-lint v2.14.0 报告 0 项。首轮的测试声明类型冗余已修正。 |
| `make website-sync`、`make website-gates` | 通过，生成数据无差异，网站构建 184 页。 |
| `GOOS=windows GOARCH=amd64 CGO_ENABLED=0 go test -c -o /tmp/plumego-943-log-windows.test.exe ./log` | 交叉编译通过。 |
| `GOOS=plan9 GOARCH=amd64 CGO_ENABLED=0 go test -c -o /tmp/plumego-943-log-plan9.test ./log` | 交叉编译通过。 |
| 逐张 `make check-card C=done/<卡片文件名>`，另核对编号、链接和 `git diff --check` | 53 张卡全部通过，保留历史报告的 52 项编号，核对 425 条链接。 |

Windows 和 Plan 9 的日志链接替换尚无原生运行证据。上一轮 KV 原生锁验证保留在 [1615](../blocked/1615-kv-os-lock-platform.md)。

## 本机基准

环境为 Go 1.26.0、darwin/arm64、Apple M1 Pro。下表记录三次运行的中位数，用于记录当前成本。

```sh
go test -p 1 ./router ./middleware/requestid ./log ./metrics -run '^$' -bench 'Benchmark(PathPoolDeepPaths|RequestIDPoolParallel|NormalizeJSONFieldMapLimits|MultiConstructionNilMix)$' -benchmem -benchtime=200ms -count=3
```

| 场景 | ns/op | B/op | allocs/op |
|---|---:|---:|---:|
| `BenchmarkPathPoolDeepPaths/8` | 50.15 | 0 | 0 |
| `BenchmarkPathPoolDeepPaths/64` | 842.90 | 2072 | 5 |
| `BenchmarkPathPoolDeepPaths/65` | 709.70 | 2072 | 5 |
| `BenchmarkPathPoolDeepPaths/256` | 2400.00 | 9243 | 7 |
| `BenchmarkRequestIDPoolParallel` | 972.10 | 16 | 1 |
| `BenchmarkNormalizeJSONFieldMapLimits/32` | 2545.00 | 2913 | 37 |
| `BenchmarkNormalizeJSONFieldMapLimits/2048` | 80832.00 | 98482 | 1031 |
| `BenchmarkMultiConstructionNilMix/2-stride-2` | 5.96 | 0 | 0 |
| `BenchmarkMultiConstructionNilMix/2-stride-3` | 5.96 | 0 | 0 |
| `BenchmarkMultiConstructionNilMix/8-stride-2` | 75.45 | 136 | 2 |
| `BenchmarkMultiConstructionNilMix/8-stride-9` | 15.08 | 0 | 0 |
| `BenchmarkMultiConstructionNilMix/64-stride-2` | 322.30 | 1048 | 2 |
| `BenchmarkMultiConstructionNilMix/64-stride-65` | 104.30 | 0 | 0 |

路由池阈值按切片容量衡量。切片扩容后容量可能超过同一数量的逻辑路径段。
