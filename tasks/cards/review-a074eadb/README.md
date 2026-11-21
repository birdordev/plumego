# 稳定模块质量复审实施记录

已执行并归档 **51 张**，编号 1797–1847。表中保留审查时的优先级与证据状态，每张卡附实施结论和验收用例。 [基线审查报告](../../../docs/evidence/stable-quality-review-a074eadb.md)

历史过滤问题在导出处理器复现。实施时用基线文件覆盖验证非法时间返回 200，归档后的回归以公开导出入口为准。日志同名实例并发运行采用独立目录，各目录仍可保留同一程序早先运行的归档。

| 卡片 | 优先级 | 证据状态 | 主模块 | 第 9 节检查号 |
|---|---|---|---|---|
| [1797 健康组件命名回调重入时注册互等](../done/1797-register-name-callback-lock.md) | P1 | 已复现 | x/observability | 5,29,33,42,47 |
| [1798 健康聚合把异常退出的 worker 当成成功](../done/1798-aggregate-worker-abnormal-result.md) | P1 | 已复现 | x/observability | 23,28,29,33,42,46 |
| [1799 健康重试在等待期间丢失取消原因](../done/1799-retry-delay-context-error.md) | P2 | 已复现 | x/observability | 22,26,42,46 |
| [1800 最大 RetryCount 被溢出成单次检查](../done/1800-retry-count-upper-bound.md) | P2 | 已复现 | x/observability | 14,22,35,42 |
| [1801 HEAD handler 异常退出时生成隐式成功状态](../done/1801-head-abnormal-exit-commit.md) | P2 | 已复现 | router | 23,24,37,46 |
| [1802 历史导出忽略无法解析的时间参数](../done/1802-history-invalid-time-filter.md) | P2 | 已复现 | x/observability | 18,21,22,38,46 |
| [1803 健康聚合并发限制仍为每组件创建 goroutine](../done/1803-aggregate-worker-count-bound.md) | P2 | 源码确认 | x/observability | 26,29,35,42,48 |
| [1804 共享日志目录的保留策略缺少其他实例的活动文件信息](../done/1804-retention-instance-ownership.md) | P2 | 源码确认 | log | 16,29,31,43 |
| [1806 核对生命周期入口检查自定义 Context 的锁边界](../done/1806-lifecycle-pre-cancel-reenter-policy.md) | P2 | 补证 | core | 26,28,33,36 |
| [1807 核对 handler 构建期间 Shutdown 与重试](../done/1807-shutdown-during-handler-build.md) | P2 | 补证 | core | 28,31,33,36 |
| [1808 核对旧 RouteGroup 的 handler 错误与冻结顺序](../done/1808-group-immutable-registration-precedence.md) | P2 | 补证 | core | 19,22,28,36 |
| [1809 核对关停在已取消和立即截止时的原因链](../done/1809-shutdown-context-primary-causes.md) | P2 | 补证 | core | 22,26,36,46 |
| [1811 核对 FIFO 缓存替换现有键后的逐出顺序](../done/1811-cached-result-fifo-replacement.md) | P2 | 补证 | router | 29,31,32,37 |
| [1812 核对显式方法、Any 与 HEAD 的 Allow 边界](../done/1812-router-method-any-allow.md) | P2 | 补证 | router | 6,18,37,45 |
| [1813 核对静态适配器在不提供上下文的 FS 上的边界](../done/1813-static-filesystem-open-context.md) | P2 | 补证 | router | 16,26,29,37 |
| [1814 补证 HEAD 延迟提交时的头部快照](../done/1814-head-pending-header-delivery.md) | P2 | 补证 | router | 6,23,24,37 |
| [1815 核对复用 ErrorBuilder 的多次构建快照](../done/1815-error-normalized-detail-builder-reuse.md) | P2 | 补证 | contract | 6,12,34,38 |
| [1816 核对自定义 As 回调与值优先级](../done/1816-error-shared-wrapper-as-delegation.md) | P2 | 补证 | contract | 13,22,33,38 |
| [1817 核对 nil RouteState 对父请求元数据的影响](../done/1817-nil-route-state-metadata-shadow.md) | P2 | 补证 | contract | 6,27,34,38 |
| [1818 核对正常业务头在普通编码错误后的保留](../done/1818-json-success-header-marshal-cycle.md) | P2 | 补证 | contract | 21,23,24,38 |
| [1819 核对 Chain 对 nil 最终 handler 的构建约定](../done/1819-chain-zero-final-handler-contract.md) | P2 | 补证 | middleware | 6,15,17,39 |
| [1820 核对超时 worker 异常退出与取消交叠](../done/1820-timeout-worker-goexit-and-parent-cancel.md) | P2 | 补证 | middleware | 23,26,29,39,47 |
| [1821 核对读取超限与已提交成功头后的 Flush](../done/1821-bodylimit-write-then-read-overrun.md) | P2 | 补证 | middleware | 20,23,24,39 |
| [1822 补证 CORS 决策的配置与响应边界](../done/1822-cors-repeated-origin-configured-trust.md) | P2 | 补证 | middleware | 14,18,20,39,46 |
| [1823 核对 Debug 旁路已编码错误的表示字段](../done/1823-debug-after-encoded-response.md) | P2 | 补证 | middleware | 21,23,24,39 |
| [1824 核对底层写错误后的 span 收尾参数](../done/1824-tracing-close-on-base-writer-error.md) | P2 | 补证 | middleware | 23,29,33,39 |
| [1825 核对无正文状态与实际录入的 HTTP 指标](../done/1825-httpmetrics-no-body-and-rewrite.md) | P2 | 补证 | middleware | 24,27,39,44 |
| [1826 核对 Stop 等待注入时钟的在途扫描](../done/1826-abuse-stop-overlapping-custom-scan.md) | P2 | 补证 | security | 28,29,32,40,47 |
| [1827 验证 KeyStore 32 条可见键前后的枚举](../done/1827-jwt-keystore-mixed-threshold.md) | P2 | 补证 | security | 26,34,35,40,48 |
| [1828 核对签名 key 更换后 Context 取消的发布边界](../done/1828-jwt-active-key-rotation-post-cancel.md) | P2 | 补证 | security | 26,30,31,40 |
| [1829 核对认证 Principal 组成数据的所有权](../done/1829-auth-principal-groups-alias.md) | P2 | 补证 | security | 5,27,34,40 |
| [1830 核对公开 URL 校验分类及安全诊断](../done/1830-input-public-url-invalid-shape-errors.md) | P2 | 补证 | security | 18,22,25,40 |
| [1831 核对失效读取与关闭后的统计生命周期](../done/1831-cache-expired-read-overlap-close.md) | P2 | 补证 | store | 28,32,34,41,47 |
| [1832 核对 KV 关闭时快照数据归属和计数](../done/1832-kv-cleared-after-close-counters.md) | P2 | 补证 | store | 28,29,34,41 |
| [1833 核对快照重开后时间平局的淘汰契约](../done/1833-kv-reopened-order-tie.md) | P2 | 补证 | store | 31,32,41 |
| [1834 核对池 Ping 在取消后的错误与资源归属](../done/1834-db-ping-wrapper-cancel-cleanup.md) | P2 | 补证 | store | 22,26,29,41 |
| [1835 核对 Commit 失败后事务已结束的错误链](../done/1835-db-transaction-rollback-after-commit-error.md) | P2 | 补证 | store | 22,29,30,31,41 |
| [1836 核对 File 及上传配置的 nil 与空容器语义](../done/1836-metadata-typed-empty-snapshot.md) | P2 | 补证 | store | 6,34,41 |
| [1837 核对幂等键输入规范和调用方一致性](../done/1837-idempotency-key-space-unicode-compat.md) | P2 | 补证 | store | 6,18,22,41 |
| [1838 核对同源健康详情的多次 Clone 结果](../done/1838-health-status-clone-mixed-snapshots.md) | P2 | 补证 | health | 16,34,42 |
| [1840 核对 tracker 的完成先后与历史顺序](../done/1840-tracker-history-publication-order.md) | P2 | 补证 | x/observability | 28,31,32,42 |
| [1841 核对活动链接发布失败与其后轮换的清理边界](../done/1841-log-file-link-promote-retry.md) | P2 | 补证 | log | 29,30,31,43 |
| [1842 核对文本后端错误 Stringer 与控制字符的边界](../done/1842-text-escape-typed-error-context.md) | P2 | 补证 | log | 25,33,43,46 |
| [1843 核对衍生 logger 的字段合并与诊断隔离](../done/1843-logger-scoped-fields-diagnostic-order.md) | P2 | 补证 | log | 25,32,34,43 |
| [1844 核对 HTTP 状态分类与显式 Error 标志](../done/1844-metrics-http-typed-error-priority.md) | P2 | 补证 | metrics | 6,22,44,45 |
| [1845 补证空聚合统计与正常记录同时使用](../done/1845-metrics-total-after-nil-fanout.md) | P2 | 补证 | metrics | 5,32,34,44 |
| [1846 测量读多写少时高基数统计快照分配](../done/1846-metric-collector-record-clear-gap.md) | P2 | 补证 | metrics | 32,35,44,48 |
| [1805 健康间隔配置缺少未启用调度的说明](../done/1805-manager-interval-config-doc.md) | P3 | 源码确认 | x/observability | 6,14,16,42,52 |
| [1810 核对路由自省结果的独立所有权](../done/1810-core-route-snapshot-introspection.md) | P3 | 补证 | core | 6,16,34,36 |
| [1839 核对零值就绪记录的时间和 JSON 字段](../done/1839-readiness-zero-timestamp-wire.md) | P3 | 补证 | health | 6,16,38,42 |
| [1847 核对门面 Param 对请求上下文的透传顺序](../done/1847-facade-parameter-with-context-overrides.md) | P3 | 补证 | 根门面 | 3,6,27,37 |


## 验证记录

| 命令 | 结果 |
|---|---|
| `make fmt`、`git diff HEAD --check` | 通过。 |
| `make validate-diff-dry`、`make validate-diff` | 通过，跨模块配置执行 `make gates`，含边界与 API 快照、vet、race、参考模块、覆盖率和 CLI。 |
| `make test`、稳定模块及健康宿主定向 `go test -race` | 通过。健康聚合、HEAD 和日志核心回归另以 race 模式连续运行 10 次，通过。 |
| `make lint` | 通过，0 项。 |
| `make stable-coverage` | 通过，九个稳定模块语句覆盖率 96.8%。 |
| `make website-sync`、`make website-gates` | 通过，生成数据无差异，构建 184 页。构建仍报告既有 CSS `:global` 压缩警告。 |
| 逐张运行 `make check-card C=done/<卡名>` | 51 张通过，另核对 52 个检查号和 380 条相对链接。 |
| `CGO_ENABLED=0 GOOS=windows GOARCH=amd64 go test -c ./log` 和对应的 `GOOS=plan9` | 两个目标均交叉编译成功。原生运行待平台环境验证。 |

## 本机基准

Go 1.26.0、darwin/arm64、Apple M1 Pro。下表为三次采样的中位数。KeyStore 每次读取遍历 64 个条目，并返回对应数量的未过期键。指标快照基准提前填充系列，高基数建表、读取和清空另行计量。

```sh
go test -p 1 ./security/jwt ./metrics ./x/observability/ops/healthhttp -run '^$' -bench 'Benchmark(AggregateWorkerBound|KeyStoreEnumerationThreshold|CollectorStatsSnapshot|BaseCollectorNameCardinality)$' -benchmem -benchtime=100ms -count=3
```

| 场景 | ns/op | B/op | allocs/op |
|---|---:|---:|---:|
| `BenchmarkKeyStoreEnumerationThreshold/live=31` | 1189.00 | 512 | 1 |
| `BenchmarkKeyStoreEnumerationThreshold/live=32` | 1263.00 | 512 | 1 |
| `BenchmarkKeyStoreEnumerationThreshold/live=33` | 1019.00 | 588 | 1 |
| `BenchmarkBaseCollectorNameCardinality/1` | 431.80 | 688 | 7 |
| `BenchmarkBaseCollectorNameCardinality/1000` | 103335.00 | 163851 | 31 |
| `BenchmarkCollectorStatsSnapshot/series=0` | 36.26 | 48 | 1 |
| `BenchmarkCollectorStatsSnapshot/series=32` | 927.70 | 1880 | 4 |
| `BenchmarkCollectorStatsSnapshot/series=1024` | 10019.00 | 54656 | 6 |
| `BenchmarkAggregateWorkerBound/8` | 8224.00 | 1238 | 38 |
| `BenchmarkAggregateWorkerBound/128` | 125661.00 | 18137 | 403 |
| `BenchmarkAggregateWorkerBound/512` | 639026.00 | 74521 | 1561 |


健康聚合基线使用 Go 文件覆盖载入 `a074eadb` 的 `manager.go`，在同机运行同一基准。表中记录三次采样中位数。

| 组件数 | 基线 B/op | 当前 B/op | 基线 allocs/op | 当前 allocs/op |
|---:|---:|---:|---:|---:|
| 8 | 1938 | 1238 | 49 | 38 |
| 128 | 30291 | 18137 | 653 | 403 |
| 512 | 123459 | 74521 | 2600 | 1561 |
