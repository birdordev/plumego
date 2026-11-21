# 稳定模块第 9 节质量审查

基线为 `dee7aa0f`，审查开始时工作区干净。范围为九个稳定模块及根门面。参考装配、内部 writer 和 healthhttp 只作为代表调用链取证。

本页记录修复前的基线与源码行号。每项的当前实现、回归位置及剩余验证见 [实施结果](../../tasks/cards/review-dee7aa0f/README.md)。

## P0–P3 发现

**P0 为 0 项。** 本轮未取得可定级为 P0 的证据。在选定入口核查了拒绝路径及敏感数据处理，实际部署配置和第三方调用方式未取证。

| 优先级 | 已复现 | 源码确认 | 补证任务 |
|---|---:|---:|---:|
| P0 | 0 | 0 | 0 |
| P1 | 2 | 0 | 0 |
| P2 | 0 | 8 | 45 |
| P3 | 0 | 4 | 3 |


已复现表示在本机从公开入口观察到对应行为。源码确认表示触发与结果可从代码直接追溯，包含已确认的契约、文档与容量边界问题。补证卡用于验证行为或测量证据，当前不计入缺陷数量。

### P1

- **1570 中间件构建重入导致应用准备阻塞**（已复现，§9 20,28,33,36,47）
  - 外层 Prepare 持有 serverPrepareMu 并在 handlerPrepareMu 内调用构建器，构建器重入 Prepare 时等待自身持有的锁。临时测试超时，栈停在 ensureServerPrepared；首次 ServeHTTP 也会进入同一构建路径。 [core/http_handler.go:34](../../core/http_handler.go#L34)
  - 最小方案。用明确的构建中状态和单一发布边界取代跨构建器的准备锁。重叠尝试应有明确结果，失败或 panic 释放构建资格。
  - 回归验收。用通道控制构建器重入 Prepare 和外部并发 Prepare，断言调用可返回、成功时仅发布一次，失败时仍可重试。 [任务卡](../../tasks/cards/done/1570-prepare-reentry.md)
- **1571 gzip 协商遗漏 Accept 缓存维度**（已复现，§9 6,24,39,45）
  - 普通 Accept 得到 gzip 和 Vary: Accept-Encoding；SSE Accept 得到无编码响应，且完全没有 Vary。共享缓存无法据响应头区分这两个变体。 [middleware/compression/gzip.go:71](../../middleware/compression/gzip.go#L71)
  - 最小方案。将 Accept 与 Accept-Encoding 放入同一变体声明并覆盖 SSE 直通，复用已有 Vary writer。保留升级直通。
  - 回归验收。对相同 URL 对比普通与 SSE Accept，检查编码、原有 Vary 去重及两个结果共同声明的变体维度。 [任务卡](../../tasks/cards/done/1571-gzip-accept-vary.md)

### P2

- **1572 调试错误响应默认回显完整查询**（源码确认，§9 21,25,39,46）
  - 默认 IncludeQuery 为 true，符合替换条件的响应直接携带 RawQuery。调试模式下，可能把含机密参数的查询复制进响应及其下游记录。生产使用情况未验证。 [middleware/debug/debug_errors.go:373](../../middleware/debug/debug_errors.go#L373)
  - 最小方案。默认关闭查询回显。对显式启用的路径仅输出经审核的非敏感元数据，并同步调试配置说明。
  - 回归验收。使用无秘密的测试占位值验证默认及启用路径的查询裁剪，同时保持错误状态与 request_id。 [任务卡](../../tasks/cards/done/1572-debug-query-redaction.md)
- **1573 调试包装派生另一套错误码**（源码确认，§9 6,12,21,38）
  - 404 的 StatusText 派生 not_found，而 contract.TypeNotFound 使用 resource_not_found。调试响应因此和标准错误响应产生同一类型、不同 code。 [middleware/debug/debug_errors.go:365](../../middleware/debug/debug_errors.go#L365)
  - 最小方案。先盘点调试消费者的 code 断言与兼容承诺，按授权复用 ErrorType 元数据并同步示例。
  - 回归验收。以 404 和 422 对比标准和调试包装的 code、type、status，明确旧 code 的迁移决定。 [任务卡](../../tasks/cards/done/1573-debug-error-code.md)
- **1574 KV 临时快照回滚丢弃清理错误**（源码确认，§9 22,29,30,41,46）
  - 写入和同步失败时丢弃 Close 错误；提交前退出时也丢弃临时文件删除错误。调用者只能看到主故障，无法诊断可能残留的资源。 [store/kv/persistence.go:180](../../store/kv/persistence.go#L180)
  - 最小方案。仅在提交前收集有意义的关闭和删除错误，与主因合并，忽略文件已经不存在。
  - 回归验收。注入写主错误与清理错误，断言两者可分类、原快照仍可打开，且下一次写入可用。 [任务卡](../../tasks/cards/done/1574-kv-cleanup-errors.md)
- **1575 KV 配额在全量快照解码后才生效**（源码确认，§9 14,29,35,41,48）
  - 启动先读取、解码并复扫整个文件，构造器随后才裁剪条目。当前内存配置限制驻留数据，无法限制打开过大历史快照时的瞬时内存。 [store/kv/persistence.go:25](../../store/kv/persistence.go#L25)
  - 最小方案。先定磁盘输入预算与历史超配额快照的兼容策略，再在分配前检查输入或用有界解析。
  - 回归验收。覆盖正常旧格式、临界文件和裁剪兼容，测量峰值内存，确认拒绝时目录锁可重新取得。 [任务卡](../../tasks/cards/done/1575-kv-load-budget.md)
- **1576 不可 seek 静态文件的条件请求语义不一致**（源码确认，§9 6,17,24,37）
  - 可 seek 分支委托 ServeContent，原始复制分支只设置长度和 MIME，未处理日期条件。使用不可 seek 文件系统时，已有条件请求会按完整传输处理。 [router/static.go:280](../../router/static.go#L280)
  - 最小方案。明确 fallback 的条件契约，优先补与文件时间有关的无正文响应，或文档明确 fallback 的有限能力并加断言。
  - 回归验收。用同数据的可 seek 与不可 seek 两种文件测试日期条件和 HEAD，无需网络文件系统。 [任务卡](../../tasks/cards/done/1576-static-conditional-fallback.md)
- **1577 默认指标收集器没有名称基数上限**（源码确认，§9 14,35,44,48）
  - Record 接受任意非空名称，每个新名称进入 NameBreakdown 并保留到 Clear。调用者把变化中的值作为名称时，内存与快照分配持续增长。 [metrics/collector.go:148](../../metrics/collector.go#L148)
  - 最小方案。先明确固定名称的调用契约并盘点代表调用点；经兼容决策后再选择是否提供受限收集方式。
  - 回归验收。比较固定名称和逐条变化名称的 NameBreakdown、分配和 Clear 后状态，记录经批准的容量策略。 [任务卡](../../tasks/cards/done/1577-base-series-cardinality.md)
- **1578 生命周期清理回调异常退出时中断逆序清理**（源码确认，§9 28,29,30,36）
  - 回调直接终止当前 goroutine 时 recover 无异常值，stopPending 循环随栈退出，后续待清理项要靠另一次 Stop。当前文档只精确处理 panic 与错误。 [core/lifecycle_group.go:265](../../core/lifecycle_group.go#L265)
  - 最小方案。先明确非 panic 异常退出的支持边界及重试语义，按决定记录并验证待清理标记。
  - 回归验收。隔离 goroutine 验证回调终止后组阶段和待清理项，后续 Stop 只处理未完成项。 [任务卡](../../tasks/cards/done/1578-stop-abnormal-exit.md)
- **1579 请求合并器没有活跃键数量预算**（源码确认，§9 14,29,35,39,48）
  - 每个活跃键保留请求记录直到 leader 结束，等待者的超时不结束 leader，响应体上限也不控制键总数。慢 handler 下容量取决于外层并发控制。 [middleware/singleflight/singleflight.go:209](../../middleware/singleflight/singleflight.go#L209)
  - 最小方案。明确外层并发限制的装配要求，量化并发键成本；只有获得 API 授权后才考虑独立预算。
  - 回归验收。用通道保持多个不同键活跃，验证总量及释放时机，记录推荐的已有装配路径。 [任务卡](../../tasks/cards/done/1579-singleflight-active-keys.md)

### P3

- **1580 超时 writer 的旁路命名与实际行为不符**（源码确认，§9 9,10,16,23）
  - 超过回放预算时丢弃后续写入并返回结构化 500，bypassUsed 容易被理解为透传。 [middleware/timeout/timeout.go:219](../../middleware/timeout/timeout.go#L219)
  - 最小方案。重命名私有字段并让注释描述回放拒绝。
  - 回归验收。复用现有回放容量测试，保持两个预算触发的状态、响应体和调用次数。 [任务卡](../../tasks/cards/done/1580-timeout-limit-names.md)
- **1581 静态文件说明忽略已存在的处理分支**（源码确认，§9 9,16,24）
  - 注释只说补前导斜线，函数还处理空白、尾斜线及根路径。读注册规则时容易漏掉后两项。 [router/static.go:18](../../router/static.go#L18)
  - 最小方案。在函数注释写明实际三种归一化操作。
  - 回归验收。复核 Static 和 StaticFS 的根前缀与子前缀测试，使用既有行为。 [任务卡](../../tasks/cards/done/1581-static-fallback-comment.md)
- **1582 门面示例忽略路由注册错误**（源码确认，§9 3,16,19,52）
  - GoDoc 直接调用 Get 后开始监听，未展示错误检查。新调用者照抄时可能在注册失败后继续启动。 [plumego.go:7](../../plumego.go#L7)
  - 最小方案。示例先检查注册错误，再启动服务器，补核对 Param 示例。
  - 回归验收。运行根包测试和文档片段检查，确认示例仍用批准的六个导出符号。 [任务卡](../../tasks/cards/done/1582-facade-ignore-error-example.md)
- **1631 指标模块说明误称默认收集器保留耗时**（源码确认，§9 6,16,44,52）
  - 模块说明把 ObserveHTTP 的采样形状和 BaseMetricsCollector 的聚合能力混写。默认收集器仅更新名称和错误计数，构建包含秒数的记录是 NewHTTPRecord 的职责。 [docs/modules/metrics/README.md:68](../modules/metrics/README.md#L68)
  - 最小方案。改写模块说明，分别描述基础计数聚合与标准 HTTP 记录。
  - 回归验收。核对 metrics/collector.go 中两个入口及现有测试，生成网站模块索引并检查差异（若索引内容受影响）。 [任务卡](../../tasks/cards/done/1631-observe-http-doc.md)

## 执行顺序

共 **62 张可独立验收的候审任务卡**。先推进 1570、1571 两项已复现问题，再处理 1572 的敏感输出和 1573 的错误码兼容决定。容量、契约和补证任务的验收位于 [卡片索引](../../tasks/cards/review-dee7aa0f/README.md)。

## 审查准备与模块取证

使用 `stable_root_boundary_review` 路由与 review 上下文包。读取九份模块清单、质量规则、依赖规则、兼容清单、标准参考装配。根门面和审查文档无独立目标模块清单。代码位置以基线为准。

审查文档与候审卡的写入对公开 API、依赖、运行行为和安全策略无影响。临时核验仅使用本地测试文件；扩展族实现、参考应用实现、生成产物和现有队列均在写入范围之外。文档验证使用仓库任务卡检查及 diff 门禁，另采集现有代码质量基线。

| 模块 | 主要入口与实现证据 | 代表调用方或测试 |
|---|---|---|
| core | [New](../../core/app.go#L39)、[Prepare / ServeHTTP](../../core/http_handler.go)、[LifecycleGroup](../../core/lifecycle_group.go) | [标准装配](../../reference/standard-service/internal/app/app.go)、[公开行为](../../core/core_public_test.go) |
| router | [AddRoute / Group](../../router/registration.go)、[匹配与分派](../../router/dispatch.go)、[URL](../../router/metadata.go)、[静态挂载](../../router/static.go) | [core 路由适配](../../core/routing.go)、[契约测试](../../router/router_contract_test.go) |
| contract | [WriteResponse / WriteError](../../contract/response.go)、[APIError](../../contract/errors.go)、[上下文](../../contract/context_core.go) | [标准服务](../../reference/standard-service/internal/app/routes.go)、[响应测试](../../contract/response_test.go) |
| middleware | [Chain](../../middleware/middleware.go)、[timeout](../../middleware/timeout/timeout.go)、[compression](../../middleware/compression/gzip.go)、[debug](../../middleware/debug/debug_errors.go)、[singleflight](../../middleware/singleflight/singleflight.go) | [参考装配](../../reference/standard-service/internal/app/app.go)、[writer 组合](../../middleware/conformance/response_writer_contract_test.go) |
| security | [VerifyToken](../../security/jwt/verify.go)、[轮换](../../security/jwt/keys.go)、[StaticToken](../../security/authn/token.go)、[密码](../../security/password/password.go)、[limiter](../../security/abuse/limiter.go) | [认证适配](../../middleware/auth/auth.go)、[JWT 负向](../../security/jwt/jwt_negative_matrix_test.go) |
| store | [KV](../../store/kv/kv.go)、[持久化](../../store/kv/persistence.go)、[DB](../../store/db/sql.go)、[缓存](../../store/cache/memory.go)、[文件元数据](../../store/file/types.go)、[幂等验证](../../store/idempotency/store.go) | [KV 状态回归](../../store/kv/state_regression_test.go)、[事务回归](../../store/db/transaction_regression_test.go) |
| health | [HealthState / HealthStatus](../../health/core.go)、[ReadinessStatus](../../health/readiness.go) | [模型测试](../../health/health_test.go)、[healthhttp 管理器](../../x/observability/ops/healthhttp/manager.go) |
| log | [NewLogger](../../log/logger.go#L92)、[JSON 后端](../../log/json.go)、[文本后端](../../log/glog_writer.go) | [accesslog](../../middleware/accesslog/accesslog.go)、[降级测试](../../log/safety_regression_test.go) |
| metrics | [BaseMetricsCollector](../../metrics/collector.go)、[MultiCollector](../../metrics/multi.go) | [httpmetrics](../../middleware/httpmetrics/http_metrics.go)、[并发回归](../../metrics/concurrency_regression_test.go) |
| 根门面 | [六个批准的入口](../../plumego.go) | [hello 示例](../../examples/hello/main.go)、[根包回归](../../plumego_test.go) |

边界门禁通过。质量审查的优先处理点是应用准备的回调锁边界、HTTP 变体和调试输出。平台差异、真实生产配置、应用私有调用方和性能峰值仍以补证卡记录。

## 关键状态与提交点

| 路径 | 初始状态与事件 | 成功后的状态 | 失败后保留状态与可用重试 |
|---|---|---|---|
| App 准备 | 未发布 handler，开始构建时冻结注册 | handler 和最后成功阶段一起发布，显式准备随后发布服务器 | 构建返回 nil 时 handler 未发布，注册冻结，后续可重试准备。已复现的重入在构建结束前阻塞。 |
| LifecycleGroup | 尚未开始，按注册顺序启动并记录待清理项 | 全部启动后进入运行态并可探测；关闭按逆序释放 | 启动或回滚失败后进入非活动状态。Stop 可重试剩余项。非 panic 异常退出会中断当轮逆序遍历，见 1578。 |
| KV 写入 | 复制内存索引并暂存变更，再生成临时快照 | Rename 发布新快照；目录同步确认持久化 | 替换前失败恢复原索引，替换后同步失败保留发布。核对当前状态后决定是否重试。临时文件清理错误的可观测性见 1574。 |
| Timeout | worker 只拥有私有缓冲，外层等待结果或取消 | 接收结果且未观察到取消时由外层提交缓冲 | 已观察取消提交结构化错误，迟到 worker 无外层响应所有权。非协作式 worker 的退出仍取决于 handler。 |
| 指标聚合 | 各子收集器持有自己的状态 | 每个子快照独立一致，聚合取样跨越子收集器调用 | 重置可被读取观察为多个阶段，最终清空由所有子收集器各自完成。 |

## 第 9 节逐项证据

| 编号 | 审查项 | 证据与核验边界 |
|---|---|---|
| 01 | 模块归属 | 已读九份 `module.yaml`，按各入口及调用关系确定归属。健康探测调度由 [healthhttp](../../x/observability/ops/healthhttp/manager.go) 承担。 |
| 02 | 依赖方向 | `make boundary-check` 通过。稳定包、内部工具允许调用方与跨扩展依赖由 [依赖规则](../../specs/dependency-rules.yaml) 检查。 |
| 03 | 根门面 | [plumego.go](../../plumego.go) 公开四个函数及两个别名，转发到 core/router。示例错误处理见 1582。 |
| 04 | 标准库和内部调用 | 根 [go.mod](../../go.mod) 无依赖声明，边界门禁通过。内部 HTTP 实用工具按清单核对；已有 1535 的架构评估继续独立处理。 |
| 05 | 显式构造 | [core.New](../../core/app.go#L39) 与 [标准参考装配](../../reference/standard-service/internal/app/app.go) 用字段注入。requestid 具备清单明确允许的默认生成器，调用者可显式替换。 |
| 06 | 稳定调用契约 | 九份 API 快照通过；签名与结构以快照为准，默认值、错误码、缓存变体还需行为证据。见 1571、1573、1576。 |
| 07 | 兼容层处置 | [废弃清单](../../specs/deprecation-inventory.yaml) 严格检查通过。核对了 KV Config/Options 兼容路径及 httpmetrics 的 Observer 别名，保留决定按清单。 |
| 08 | 可审查与可回滚 | [候审卡](../../tasks/cards/review-dee7aa0f/README.md) 每卡一个主模块。跨模块观测仅在测试消费链中使用相邻模块；跨模块契约变更需独立规划。 |
| 09 | 命名意图 | [timeout writer](../../middleware/timeout/timeout.go#L218) 的旁路字段实际触发回放拒绝。见 1580。 |
| 10 | 主流程 | 对照 [超时选定响应](../../middleware/timeout/timeout.go#L144) 与 [core 构建阶段](../../core/http_handler.go#L14)。构建回调和最终发布需清晰分段，见 1570。 |
| 11 | 职责与阶段 | [KV 变更](../../store/kv/kv.go#L113) 与 [快照替换](../../store/kv/persistence.go#L165) 之间有明确阶段，清理信息缺口见 1574。 |
| 12 | 辅助函数 | [标准错误元数据](../../contract/errors.go#L73) 已集中，调试包装仍从状态文案派生 code。见 1573。 |
| 13 | 小接口 | [metrics](../../metrics/collector.go#L66) 提供独立观测、统计与重置；[core](../../core/lifecycle_group.go#L31) 按阶段声明接口。检查了标准装配和观测消费者，未遍历所有外部使用方。 |
| 14 | 配置语义 | 核对 [core 默认值](../../core/config.go)、[缓存配置](../../store/cache/config.go) 及 [timeout 默认值](../../middleware/timeout/timeout.go#L77)。名称基数和活动键预算见 1577、1579。 |
| 15 | 构造错误 | [KV](../../store/kv/kv.go#L71) 及 [安全头](../../middleware/securityheaders/headers.go#L50) 构造返回错误，路由分组的既存 panic 契约按兼容处理。TLS 恢复补证见卡目录。 |
| 16 | 约束文档 | [health 克隆](../../health/core.go#L44) 明确写了浅复制，[metrics](../modules/metrics/README.md) 的耗时说明需更正。路由归一化、超时命名和门面示例见 1580–1582。 |
| 17 | net/http 兼容 | [Router.ServeHTTP](../../router/dispatch.go#L155) 和 [middleware.Chain](../../middleware/middleware.go#L129) 保持标准类型。实际 writer 组合在 24、37、39 项评估。 |
| 18 | 输入来源 | [路由静态文件](../../router/static.go#L33) 明确从路由参数取值，[认证 token](../../security/authn/token.go#L37) 从 Authorization 取值；调试查询与正文上限的输入边界见卡。 |
| 19 | 显式注册 | [标准参考路由](../../reference/standard-service/internal/app/routes.go) 显式列出方法、路径、处理器与错误。门面示例遗漏错误检查，见 1582。 |
| 20 | 中间件控制流 | [并发队列](../../middleware/concurrencylimit/concurrency_limit.go) 的拒绝返回和槽位释放可见；[组合回归](../../middleware/conformance/response_writer_contract_test.go) 已运行。CORS 和 bodylimit 的专门路径仍补证。 |
| 21 | 标准 JSON 路径 | [响应入口](../../contract/response.go) 与调试包装已检查。已确认一个 code 漂移，见 1573；router 404 和 405 各保留既有传输契约。 |
| 22 | 错误可分类 | [APIError.Unwrap](../../contract/errors.go#L182)、[事务](../../store/db/sql.go#L255) 和 [KV 清理](../../store/kv/persistence.go#L165) 对照。前两项有现有测试，KV 丢弃清理错误见 1574。 |
| 23 | 响应提交 | [contract](../../contract/response.go) 先编码再提交，[recovery](../../middleware/recovery/recover.go#L60) 检查已提交状态。联合短写、回放写失败及层叠控制器待补证。 |
| 24 | writer 能力 | [HEAD](../../router/head_regression_test.go) 和 [中间件组合](../../middleware/conformance/response_writer_contract_test.go) 有 Flush、1xx 等回归；静态 fallback、gzip 变体与多层链路留下卡片。 |
| 25 | 敏感数据 | 对照 [调试错误详情](../../middleware/debug/debug_errors.go#L363)、[JWT 验证](../../security/jwt/verify.go) 与 [JSON 日志降级](../../log/json.go#L260)。发现调试查询回显；上游自定义日志数据和生产启用状况未取证。 |
| 26 | 取消传播 | [SQL](../../store/db/sql.go)、[JWT 轮换](../../security/jwt/keys.go#L105)、[timeout](../../middleware/timeout/timeout.go#L113) 已沿调用链检查。KV 文档界定同步文件阶段；事务提交交错需补证。 |
| 27 | 上下文用途 | [contract 上下文键](../../contract/context_core.go#L84)、[trace](../../contract/trace.go#L50) 与 [principal](../../security/authn/authn.go) 采用包内类型及显式访问器。RouteState 借用和多指标消费者待补证。 |
| 28 | 状态转换 | [App](../../core/http_handler.go)、[LifecycleGroup](../../core/lifecycle_group.go)、[KV/Cache](../../store/kv/kv.go) 的初始化、运行、关闭分阶段阅读。准备重入及异常退出见 1570、1578。 |
| 29 | 资源归属 | [Shutdown drain](../../core/lifecycle.go)、[超时 worker](../../middleware/timeout/timeout.go#L130)、[目录锁](../../store/kv/kv.go#L88) 及 [快照临时文件](../../store/kv/persistence.go#L172) 有明确创建点。清理重试及跨系统运行待补证。 |
| 30 | 主因和清理错误 | [事务](../../store/db/sql.go#L263) 与 [生命周期回滚](../../core/lifecycle_group.go#L145) 组合主因和清理错误。KV 的遗漏见 1574。 |
| 31 | 提交点和重试 | [KV 替换](../../store/kv/persistence.go#L195) 后同步失败保留内存发布，现有回归通过；[事务](../../store/db/sql.go#L291) 的提交竞争和 [请求合并](../../middleware/singleflight/singleflight.go#L360) 的跨代仍需补证。 |
| 32 | 并发不变量 | 九根模块及门面通过 race。基础 metrics 统计共用一把锁；请求合并、注册/冻结、关闭与统计还有定向交错任务。 |
| 33 | 回调锁边界 | [LifecycleGroup](../../core/lifecycle_group.go) 用阶段独占并在组锁之外运行回调。[App handler 构建](../../core/http_handler.go#L19) 在准备锁内运行外部构建器，1570 已复现阻塞。 |
| 34 | 可变数据所有权 | [Routes](../../router/metadata.go#L174)、[health.Clone](../../health/core.go#L45)、[metrics fan-out](../../metrics/multi.go#L48) 的复制语义已对照。自定义错误详情、路由状态和文件元数据别名待补证。 |
| 35 | 资源增长 | [路由缓存](../../router/cache.go) 限条目、[timeout](../../middleware/timeout/timeout.go#L254) 限回放。KV 快照读取、指标名称基数、活跃合并键的限制见 1575、1577、1579。 |
| 36 | core 生命周期 | [生命周期组测试](../../core/lifecycle_group_test.go) 含逆序回滚、仅重试未清理对象和未启动取消；[公开 App 测试](../../core/core_public_test.go) 含服务准备/关停。重入缺陷和非 panic 退出边界见 1570、1578。 |
| 37 | router 匹配优先级 | [matchBranch](../../router/matcher.go#L34) 从静态到参数再到通配且回溯；[公开路由测试](../../router/router_contract_test.go) 覆盖 HEAD 与 Allow，通配路径 URL 往返临时检查通过。缓存交错、静态 fallback 待补证。 |
| 38 | contract 传输契约 | [response_test.go](../../contract/response_test.go) 覆盖 2xx、空值、编码异常、短写和裸 HTTP 的无正文帧；组合 writer 和错误叠加仍缺专项证据。 |
| 39 | middleware 组合 | [标准参考装配](../../reference/standard-service/internal/app/app.go) 显式定义顺序；[组合测试](../../middleware/conformance/response_writer_contract_test.go) 通过。缺陷见 1571–1573，异步和观测顺序另有卡片。 |
| 40 | security 拒绝路径 | [JWT 验证](../../security/jwt/verify.go)、[静态 token](../../security/authn/token.go)、[密码](../../security/password/password.go) 有拒绝/恒定时间比较的源码和已有测试。未独立测量 timing 或验证全部生产调用方。 |
| 41 | store 持久一致性 | [KV 状态回归](../../store/kv/state_regression_test.go) 包含提交前回滚、提交后同步失败、重开和取消。历史格式、平台锁、临时文件清理和 DB 结果集分项跟进。 |
| 42 | health 准确性 | [模型](../../health/core.go#L63) 未知状态不就绪，降级状态可就绪；消费侧 [代际、在途及关闭测试](../../x/observability/ops/healthhttp/state_regression_test.go) 作为集成证据，组合顺序另有补证卡。 |
| 43 | log 输出完整性 | [JSON 编码](../../log/json.go#L204) 保留 RawMessage 数字精度，[文本转换](../../log/logger.go#L261) 有深度限制。复用过往宽度、循环及降级测试；目的地短写和总预算待补证。 |
| 44 | metrics 统计一致性 | [单收集器](../../metrics/collector.go#L141) 更新、读、清理共用锁；[聚合](../../metrics/multi.go#L82) 单独读取子快照。已有 race 不变量和标签测试。本轮追加聚合 Reset 交错与 HTTP 入口对照任务。 |
| 45 | 聚焦回归 | 本轮本地核验含两个独立发现，分别落在 1570、1571。当前回归未覆盖这些触发条件。其余卡片列出了公开入口和预期结果。 |
| 46 | 负向场景 | 用已有负向证据覆盖无效认证、取消、短写和 KV 失败。组合型负向及平台故障按卡片补证。 |
| 47 | 确定性并发 | 现有 suite 既有通道、合成时钟，也有实际延时用例。[队列竞争](../../middleware/concurrencylimit/cancel_race_test.go) 与 [limiter 清理](../../security/abuse/limiter_test.go) 已列出改进。 |
| 48 | 性能证据 | 阅读了路由、KV、日志与指标现有 benchmark 入口。本轮未做性能前后对照；卡片列出可复跑的指标和保持语义的条件。 |
| 49 | 格式与 lint | `make fmt-check` 通过。无 Go 改动，无需运行会写入源码的 `make fmt`。`make lint` 缺少工具，见补证卡。 |
| 50 | Diff 门禁 | 文档和任务卡批次用 `make validate-diff-dry` 与 `make validate-diff` 核对；稳定模块 race、vet、边界和 API 门禁另外作为代码基线。 |
| 51 | 覆盖率与关键行为 | `make stable-coverage` 测得 **96.4%**。并发、取消、异常退出和平台差异由各项证据补充。 |
| 52 | 同步与交付 | 九份稳定快照通过。审查输出记录基线、状态、修复和待取证路径。后续如修改模块原始文档，执行卡内注明的网站同步与差异核对。 |

## 实测验证

环境为 Go 1.26.0，darwin/arm64。首次运行受默认 Go 缓存权限限制，随后使用 `GOCACHE=/tmp/plumego-review-go`。

| 命令或核验 | 状态 | 结果与后续 |
|---|---|---|
| `go test -race -timeout 90s ./core/... ./router/... ./contract/... ./middleware/... ./security/... ./store/... ./health/... ./log/... ./metrics/... .` | 通过 | 九根及根门面的现有 race suite 通过。 |
| `go vet ./core/... ./router/... ./contract/... ./middleware/... ./security/... ./store/... ./health/... ./log/... ./metrics/... .` | 通过 | 无诊断。 |
| `make boundary-check api-check fmt-check stable-coverage` | 通过 | 九份 API 快照匹配，格式和边界检查通过，稳定模块总覆盖率 96.4%。 |
| `go test -race -timeout 60s ./x/observability/ops/healthhttp` | 通过 | 对健康模型的代表编排消费方取证。 |
| `make check-card` | 通过 | 对本批次 62 张卡逐张运行，另核对检查号 01–52、文件和行号链接及尾随空白。 |
| `make validate-diff-dry` 与 `make validate-diff` | 通过 | 选中 `docs_only`，该 profile 没有执行命令。 |
| `make lint` | 未运行成功 | PATH 中缺少 `golangci-lint`，在有 CI 固定版本工具的环境补跑。 |
| `go test -v -timeout 5s /tmp/plumego-quality-review_test.go` | 预期失败 | 1570 的本地对照超时，栈停在 `ensureServerPrepared` 的准备互斥锁。1571 的对照记录普通 Accept 使用 gzip 及 `Vary: Accept-Encoding`，SSE Accept 无编码也无 Vary。通配参数含重复斜线的 URL 往返对照通过。临时文件未纳入仓库。 |

覆盖率及 race suite 仅说明已执行路径，待补证范围以各卡记录为准。本轮没有运行完整根模块、CLI、参考应用、网站构建或跨平台矩阵。
