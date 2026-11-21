# 稳定模块质量审查实施结果

本批共 **62 张**任务卡，来源见 [审查报告](../../../docs/evidence/stable-quality-review-dee7aa0f.md)。已完成 61 张。1615 在 macOS 和 Linux 上通过运行测试，Windows 和 Plan 9 只完成测试编译，仍待原生运行证据。现有 `active/` 队列未调整。

每张卡记录已交付行为、测试和未验证范围。完成卡归档到 `done/`，待原生平台验证卡位于 `blocked/`。

| 卡片 | 优先级 | 证据 | 主模块 | 检查号 |
|---|---|---|---|---|
| [1570 中间件构建重入导致应用准备阻塞](../done/1570-prepare-reentry.md) | P1 | 已复现 | core | 20,28,33,36,47 |
| [1571 gzip 协商遗漏 Accept 缓存维度](../done/1571-gzip-accept-vary.md) | P1 | 已复现 | middleware | 6,24,39,45 |
| [1572 调试错误响应默认回显完整查询](../done/1572-debug-query-redaction.md) | P2 | 源码确认 | middleware | 21,25,39,46 |
| [1573 调试包装派生另一套错误码](../done/1573-debug-error-code.md) | P2 | 源码确认 | middleware | 6,12,21,38 |
| [1574 KV 临时快照回滚丢弃清理错误](../done/1574-kv-cleanup-errors.md) | P2 | 源码确认 | store | 22,29,30,41,46 |
| [1575 KV 配额在全量快照解码后才生效](../done/1575-kv-load-budget.md) | P2 | 源码确认 | store | 14,29,35,41,48 |
| [1576 不可 seek 静态文件的条件请求语义不一致](../done/1576-static-conditional-fallback.md) | P2 | 源码确认 | router | 6,17,24,37 |
| [1577 默认指标收集器没有名称基数上限](../done/1577-base-series-cardinality.md) | P2 | 源码确认 | metrics | 14,35,44,48 |
| [1578 生命周期清理回调异常退出时中断逆序清理](../done/1578-stop-abnormal-exit.md) | P2 | 源码确认 | core | 28,29,30,36 |
| [1579 请求合并器没有活跃键数量预算](../done/1579-singleflight-active-keys.md) | P2 | 源码确认 | middleware | 14,29,35,39,48 |
| [1583 补证构建失败后的公开准备状态](../done/1583-prepare-registration-freeze.md) | P2 | 补证 | core | 6,28,31,36,46 |
| [1584 补证启动取消与回滚错误组合](../done/1584-startup-cancel-cleanup.md) | P2 | 补证 | core | 22,26,30,36,46 |
| [1585 补证 TLS 配置失败恢复后再准备](../done/1585-tls-failure-then-retry.md) | P2 | 补证 | core | 14,15,28,36 |
| [1586 验证并发关停的诊断协程所有权](../done/1586-shutdown-drain-overlap.md) | P2 | 补证 | core | 28,29,32,36,47 |
| [1587 在真实包装链验证 HEAD 控制器](../done/1587-head-layered-controller.md) | P2 | 补证 | router | 17,23,24,37 |
| [1588 补证 GET、HEAD 与 Any 的冷热缓存矩阵](../done/1588-all-methods-cache-matrix.md) | P2 | 补证 | router | 31,32,37,45 |
| [1589 补证自定义静态文件系统部分打开所有权](../done/1589-static-partial-open.md) | P2 | 补证 | router | 24,29,37,46 |
| [1590 测量缓存路径长度和 FIFO 驱逐成本](../done/1590-cache-cost-by-path.md) | P2 | 补证 | router | 35,37,48 |
| [1591 补证错误详情编码失败叠加短写](../done/1591-fallback-short-write.md) | P2 | 补证 | contract | 21,22,23,38,46 |
| [1592 补证 204 与 205 在缓冲 writer 中的帧信息](../done/1592-bodyless-wrapper-headers.md) | P2 | 补证 | contract | 17,23,24,38 |
| [1593 明确 RouteState 在公开上下文中的借用约定](../done/1593-route-metadata-ownership.md) | P2 | 补证 | contract | 16,27,32,34,37 |
| [1594 补证详情拷贝的自定义值边界](../done/1594-detail-clone-noncontainer.md) | P2 | 补证 | contract | 16,23,34,38 |
| [1595 补证超时缓冲回放写失败的提交权](../done/1595-timeout-replay-failure.md) | P2 | 补证 | middleware | 21,23,24,39,46 |
| [1596 补证超时后 worker 的退出和回调归属](../done/1596-timeout-late-exit.md) | P2 | 补证 | middleware | 26,29,33,39,47 |
| [1597 补证请求体上限的已声明长度与惰性读取](../done/1597-bodylimit-contentlength.md) | P2 | 补证 | middleware | 18,20,35,39 |
| [1598 核对 CORS 预检方法大小写语义](../done/1598-cors-method-case.md) | P2 | 补证 | middleware | 6,14,20,39 |
| [1599 验证 gzip 写失败的字节计数与收尾](../done/1599-gzip-short-write.md) | P2 | 补证 | middleware | 22,23,24,39 |
| [1600 补证观测链顺序与路由上下文快照](../done/1600-observer-composition.md) | P2 | 补证 | middleware | 20,24,27,39,47 |
| [1601 补证同键下一代请求与旧等待者的隔离](../done/1601-singleflight-overlap-generation.md) | P2 | 补证 | middleware | 28,31,32,39,47 |
| [1602 量化时钟回拨下请求 ID 生成等待](../done/1602-request-id-clock-skew.md) | P2 | 补证 | middleware | 14,16,29,35,48 |
| [1603 确定性覆盖并发队列退出与槽位回收](../done/1603-concurrency-queue-determinism.md) | P2 | 补证 | middleware | 26,29,32,39,47 |
| [1604 补证轮换等待的取消与后续可用性](../done/1604-jwt-rotation-cancel.md) | P2 | 补证 | security | 26,28,31,32,40 |
| [1605 补证 JWT 内存存储对输入输出的所有权](../done/1605-jwt-store-copies.md) | P2 | 补证 | security | 26,34,40,46 |
| [1606 核对 Principal 多读者共享边界](../done/1606-auth-principal-ownership.md) | P2 | 补证 | security | 16,27,34,40 |
| [1607 补证密码校验公开错误分类](../done/1607-password-api-error-classes.md) | P2 | 补证 | security | 22,25,40,46 |
| [1608 补证编译安全头策略与源配置隔离](../done/1608-headers-policy-snapshot.md) | P2 | 补证 | security | 14,15,34,40 |
| [1609 补证 limiter Stop 的多调用者同步](../done/1609-abuse-stop-cleanup.md) | P2 | 补证 | security | 28,29,32,40,47 |
| [1610 锁定 URL 字面校验与调用方责任](../done/1610-url-validation-boundary.md) | P2 | 补证 | security | 16,18,40,46 |
| [1611 补证事务提交遇取消时的错误和终态](../done/1611-db-commit-cancel.md) | P2 | 补证 | store | 22,26,30,31,41,47 |
| [1612 记录 ScanRows 结果集边界](../done/1612-db-scan-resultsets.md) | P2 | 补证 | store | 6,16,22,29,41 |
| [1613 补证内存缓存关停后的统计语义](../done/1613-cache-close-stats.md) | P2 | 补证 | store | 28,29,32,34,41 |
| [1614 补证容量失败后 Append 的字节和 TTL](../done/1614-cache-append-limit.md) | P2 | 补证 | store | 31,34,35,41,46 |
| [1615 补证跨进程目录锁的平台差异](../blocked/1615-kv-os-lock-platform.md) | P2 | 补证 | store | 28,29,41,46,50 |
| [1616 测量 KV 写入规模与峰值](../done/1616-kv-write-allocation.md) | P2 | 补证 | store | 29,35,41,48 |
| [1617 补证文件元数据克隆的共享引用语义](../done/1617-file-clone-shape.md) | P2 | 补证 | store | 16,34,41,46 |
| [1618 补证幂等完成的错误优先级](../done/1618-idempotency-expiry-order.md) | P2 | 补证 | store | 6,16,22,31,41 |
| [1619 补证未知状态模型的序列化与判断](../done/1619-health-model-wire.md) | P2 | 补证 | health | 6,16,42,45 |
| [1620 复核就绪状态在宿主中的终止边界](../done/1620-readiness-consumer-order.md) | P2 | 补证 | x/observability | 28,32,42,47 |
| [1622 补证 JSON 衍生日志的共用输出串行化](../done/1622-json-output-shared.md) | P2 | 补证 | log | 32,33,43,47 |
| [1623 测量 JSON 节点预算跨字段成本](../done/1623-json-budget-record-cost.md) | P2 | 补证 | log | 35,43,48 |
| [1624 补证文本日志多目的地短写](../done/1624-text-injected-writer.md) | P2 | 补证 | log | 22,30,43,46 |
| [1626 验证多收集器分阶段重置的可观察性](../done/1626-aggregate-reset-order.md) | P2 | 补证 | metrics | 28,32,44,47 |
| [1627 补证 fan-out 传播调用上下文身份](../done/1627-fanout-context.md) | P2 | 补证 | metrics | 26,27,34,44 |
| [1628 量化并验证 HTTP 两入口统计一致性](../done/1628-http-classification-parity.md) | P2 | 补证 | metrics | 6,32,44,45 |
| [1630 在具备 lint 工具的环境记录基线](../done/1630-lint-stable-baseline.md) | P2 | 补证 | 质量门禁 | 2,4,49,50,51,52 |
| [1580 超时 writer 的旁路命名与实际行为不符](../done/1580-timeout-limit-names.md) | P3 | 源码确认 | middleware | 9,10,16,23 |
| [1581 静态文件说明忽略已存在的处理分支](../done/1581-static-fallback-comment.md) | P3 | 源码确认 | router | 9,16,24 |
| [1582 门面示例忽略路由注册错误](../done/1582-facade-ignore-error-example.md) | P3 | 源码确认 | 根门面 | 3,16,19,52 |
| [1631 指标模块说明误称默认收集器保留耗时](../done/1631-observe-http-doc.md) | P3 | 源码确认 | metrics | 6,16,44,52 |
| [1621 记录就绪组件表的空值语义](../done/1621-health-components-wire-shape.md) | P3 | 补证 | health | 6,16,34,42 |
| [1625 锁定日志预置字段与临时字段优先级](../done/1625-field-override-contract.md) | P3 | 补证 | log | 6,16,34,43 |
| [1629 用公开入口核对门面配置说明](../done/1629-facade-server-defaults.md) | P3 | 补证 | 根门面 | 3,6,14,16,52 |

## 本地验证

- `make gates` 通过，包含边界、API 快照、Go 文档片段、vet、race、参考服务、CLI 门禁和稳定覆盖率。覆盖率为 **96.4%**。
- 最终工作区通过 `make validate-diff`，选中 `cross_module_public_api_release`。
- `make lint` 使用 CI 固定的 v2.14.0，结果为零发现。
- `make website-sync`、`make website-check-generated` 通过，生成文件无差异。
- 1615 的原生 Windows 互斥和不支持平台的拒绝路径尚需目标环境运行。跨编译只验证编译兼容性。

## 性能样本

在 Apple M1 Pro，Go 1.26.0，darwin/arm64 上运行三次。这里记录中位数，覆盖路由路径长度与工作集、KV 快照写入规模、JSON 每字段预算及指标名称基数。基准未用于主张性能改善。

```sh
go test -p 1 -run '^$' -bench 'Benchmark(RouteCachePathLength|KVWriteSnapshotScale|JSONRecordFieldBudget|BaseCollectorNameCardinality|BaseCollectorObserveHTTP|BaseCollectorRecord)$' -benchmem -benchtime=150ms -count=3 ./router ./store/kv ./log ./metrics
```

| 场景 | ns/op | B/op | allocs/op | 快照字节 |
|---|---:|---:|---:|---:|
| RouteCachePathLength/length=16/paths=1 | 41.22 | 32 | 1 | — |
| RouteCachePathLength/length=16/paths=101 | 275.80 | 464 | 3 | — |
| RouteCachePathLength/length=256/paths=1 | 78.63 | 288 | 1 | — |
| RouteCachePathLength/length=256/paths=101 | 491.80 | 720 | 3 | — |
| RouteCachePathLength/length=2048/paths=1 | 378.40 | 2304 | 1 | — |
| RouteCachePathLength/length=2048/paths=101 | 1809.00 | 2738 | 3 | — |
| KVWriteSnapshotScale/10 | 11931003.00 | 7432 | 55 | 3012 |
| KVWriteSnapshotScale/100 | 12088439.00 | 53639 | 326 | 27492 |
| KVWriteSnapshotScale/1000 | 12471558.00 | 569368 | 3030 | 273192 |
| JSONRecordFieldBudget/1 | 155815.00 | 56134 | 2066 | — |
| JSONRecordFieldBudget/32 | 5084241.00 | 1871534 | 65658 | — |
| BaseCollectorRecord | 26.67 | 0 | 0 | — |
| BaseCollectorObserveHTTP | 22.41 | 0 | 0 | — |
| BaseCollectorNameCardinality/1 | 354.00 | 688 | 7 | — |
| BaseCollectorNameCardinality/1000 | 78068.00 | 163848 | 31 | — |

Linux arm64 容器中，打开 8000 条并裁剪到 4000 条的现有 KV 快照基准约为 15.07 ms/op、7.43 MB/op。由 `/proc/<pid>/status` 采样的进程峰值 RSS 为 11244 KiB，包含测试进程和运行时。
