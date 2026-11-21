# 稳定模块第 9 节质量复审

基线为 `2b94cf86`（完成健康统计与传输边界复审修复）。范围为九个稳定模块和根门面，另检查健康宿主与标准服务的对应调用链。

本文保留基线观察，可用 `git show 2b94cf86:<路径>` 查看修复前版本。源码链接定位当前对应入口，实际回归与基准见 [实施记录](../../tasks/cards/review-2b94cf86/README.md)。

## P0–P3 发现

确认 **7 项问题**，其中 5 项由本机合成测试复现，2 项由源码确认且分页分配已有基准。余下 46 项为定向补证。本轮未确认 P0。

| 优先级 | 已复现 | 源码确认 | 补证任务 |
|---|---:|---:|---:|
| P0 | 0 | 0 | 0 |
| P1 | 1 | 0 | 0 |
| P2 | 4 | 1 | 38 |
| P3 | 0 | 1 | 8 |

复现仅针对本机合成数据。源码确认描述可从调用路径直接推出的行为。补证项的验收结果尚待执行。

### P0

未确认。真实凭据、生产配置和部署可达性未取证。

### P1

- **1900 探测异常后健康报告仍返回已过时的健康状态**（已复现，检查号 22,28,31,32,42,46）
  - 组件先得到 healthy 快照，下一批 checker 提前退出。聚合返回 unhealthy，就绪变为 false，但同一 Tracker 的报告仍读取旧组件快照并返回 HTTP 200 和 healthy。报表消费者可能继续按健康处理。 [x/observability/ops/healthhttp/manager.go:266](../../x/observability/ops/healthhttp/manager.go#L266)
  - 最小方案。在注册身份和检查版本都匹配时，把已尝试但异常退出的固定失败终态发布到组件缓存。保留最后实际探测的语义，未尝试项以及被替换的旧项不写入。
  - 验收。本机覆盖异常退出、正常恢复、注销与替换、重叠组件检查和手工就绪。普通未执行任务不制造新样本，最新失败报告与聚合状态一致。 [任务卡](../../tasks/cards/done/1900-aborted-batch-cached-health-publication.md)

### P2

- **1901 奇数样本窗口把全成功组件标成改善**（已复现，检查号 6,12,42,44,46）
  - 三个检查全部成功时 TrendScore 为 1.5，Direction 为 improving。新半区与旧半区样本数不同，计算分母用反。 [x/observability/ops/healthhttp/tracker.go:399](../../x/observability/ops/healthhttp/tracker.go#L399)
  - 最小方案。分别用所属半区的样本数计算成功率，保留阈值与小于 3 条时的初始规则。
  - 验收。全成功、全失败、改善和下降对照 3、4、9、10 条窗口，得分介于 -1 与 1。 [任务卡](../../tasks/cards/done/1901-trend-odd-window-denominators.md)
- **1902 历史统计按追加位置推算时间，跨度可能为负**（已复现，检查号 6,31,34,42,46）
  - 注入 Manager 连续返回三条时间递减的普通记录，统计把头尾分别标为 oldest/newest，TimeSpan 为 -2 秒。历史列表按完成追加顺序有意保留，两者语义不同。 [x/observability/ops/healthhttp/tracker.go:638](../../x/observability/ops/healthhttp/tracker.go#L638)
  - 最小方案。在已有统计遍历中选取时间极值，明确同时间的稳定选择规则，单独复制极值对象。
  - 验收。逆序、混序、同时间和空历史均有可复验结果，列表与分页顺序不受影响。 [任务卡](../../tasks/cards/done/1902-history-stats-chronological-extrema.md)
- **1903 取消的空健康批次丢失就绪诊断原因**（已复现，检查号 22,26,28,42,46）
  - 取消的空批次返回 message 为 context canceled，就绪拒绝原因却为 components failed: []。调用方无法区分上下文终止和检查失败。 [x/observability/ops/healthhttp/manager.go:402](../../x/observability/ops/healthhttp/manager.go#L402)
  - 最小方案。优先给取消或截止分支使用固定分类文字；有失败组件时使用分类和名称，其余分支用确定的不可用原因。
  - 验收。分别覆盖预取消、截止、正常组件失败与空成功批，保留手工就绪和并发版本检查。 [任务卡](../../tasks/cards/done/1903-readiness-cancellation-fixed-reason.md)
- **1904 gzip 在底层不支持 Flush 时提前提交并关闭压缩选择**（已复现，检查号 17,23,24,39,46）
  - 普通 writer 没有 Flush 能力。handler 声明 202 后试用 ResponseController.Flush，收到 ErrNotSupported，但底层已提交 202，随后正常写入被改成 identity。 [middleware/compression/gzip.go:481](../../middleware/compression/gzip.go#L481)
  - 最小方案。当响应未提交、尚未启用 gzip 且可确定底层缺少能力时，尽早返回不支持。保留真实 Flush 和只有动态错误可识别的 writer 的既有提交归属。
  - 验收。覆盖显式与隐式状态、Unwrap 链、不支持的底层、有 Flush 的真实 writer 和已开启 gzip 的流，确认 fallback 可继续编码且状态只提交一次。 [任务卡](../../tasks/cards/done/1904-gzip-explicit-flush-capability-check.md)
- **1905 一条历史记录的分页请求分配整个匹配集合**（源码确认，检查号 31,34,35,42,48）
  - 分页扫描将所有匹配结构复制到临时切片，再用封顶切片返回一页。实测 limit 为 1 时，100 条和 10000 条的中位数分别为 24432 和 3579767 B/op。 [x/observability/ops/healthhttp/tracker.go:601](../../x/observability/ops/healthhttp/tracker.go#L601)
  - 最小方案。扫描时累计匹配数，仅把 offset 后命中的目标页深复制到独立小切片。避免改变过滤、归一化与完整总计语义。
  - 验收。时间、状态、组件组合筛选及边界页结果等价。对相同样本复测每页分配随页宽变化。 [任务卡](../../tasks/cards/done/1905-history-small-page-matching-set-allocation.md)

### P3

- **1906 历史导出说明缺少分页默认值与超界归一化**（源码确认，检查号 6,14,16,42,52）
  - 说明只写了整数和缺省值的处理；实现把 1–100 以外的 limit 归为 50，负 offset 归为 0。现有描述无法直接推导边界行为。 [x/observability/ops/healthhttp/tracker.go:581](../../x/observability/ops/healthhttp/tracker.go#L581)
  - 最小方案。补齐整数有效区间、归一化和非法字面值错误，不调整阈值或线上返回形状。
  - 验收。文档字段与缺省、负值、100 和 101 的 HTTP 观察一致，网站生成检查通过。 [任务卡](../../tasks/cards/done/1906-history-pagination-contract-doc.md)

## 执行顺序

先执行 1900，接续 1901、1902 和 1903。1900 用注册身份和版本护栏保护现有手工就绪行为。1904 可独立修复。1905 完成后以 1906 校对实际分页配置语义。 [实施索引](../../tasks/cards/review-2b94cf86/README.md)

## 模块取证

使用 `stable_root_boundary_review` 路由与 review 上下文包。九个稳定模块清单、健康 [宿主清单](../../x/observability/ops/module.yaml) 与稳定依赖约束均已查阅。根门面沿用根仓库约束。本轮工作区新增审查 Markdown 文件。计划将来保持公开稳定签名与标准库依赖。

| 模块 | 公共入口和内部路径 | 代表调用方及验证 |
|---|---|---|
| [core](../../core/module.yaml) | [Prepare / Shutdown](../../core/lifecycle.go)、[准备状态](../../core/http_handler.go)、[LifecycleGroup](../../core/lifecycle_group.go) | [标准服务装配](../../reference/standard-service/internal/app/app.go)、[生命周期回归](../../core/lifecycle_test.go) |
| [router](../../router/module.yaml) | [注册与冻结](../../router/registration.go)、[HEAD 与分派](../../router/dispatch.go)、[路径池](../../router/pool.go)、[反向 URL](../../router/metadata.go) | [core 路由转发](../../core/routing.go)、[路由契约](../../router/router_contract_test.go) |
| [contract](../../contract/module.yaml) | [响应入口](../../contract/response.go)、[错误链与详情快照](../../contract/errors.go)、[TraceContext](../../contract/trace.go) | [标准服务路由](../../reference/standard-service/internal/app/routes.go)、[响应回归](../../contract/response_test.go) |
| [middleware](../../middleware/module.yaml) | [Chain](../../middleware/middleware.go)、[压缩终态](../../middleware/compression/gzip.go)、[超时](../../middleware/timeout/timeout.go)、[并发](../../middleware/concurrencylimit/concurrency_limit.go) | [组合 writer 测试](../../middleware/conformance/response_writer_contract_test.go)、[访问日志](../../middleware/accesslog/accesslog.go) |
| [security](../../security/module.yaml) | [认证上下文](../../security/authn/authn.go)、[JWT 验证](../../security/jwt/verify.go)、[策略](../../security/headers/headers.go)、[限流](../../security/abuse/limiter.go) | [认证中间件](../../middleware/auth/auth.go)、[安全头适配](../../middleware/securityheaders/headers.go) |
| [store](../../store/module.yaml) | [缓存](../../store/cache/memory.go)、[KV 快照](../../store/kv/persistence.go)、[SQL](../../store/db/sql.go)、[文件和幂等契约](../../store/idempotency/store.go) | [KV 回归](../../store/kv/state_regression_test.go)、[SQL 本机桩](../../store/db/sql_test.go)、[文件快照](../../store/file/coverage_test.go) |
| [health](../../health/module.yaml) | [状态和 Clone](../../health/core.go)、[就绪模型](../../health/readiness.go) | [Manager](../../x/observability/ops/healthhttp/manager.go)、[Tracker](../../x/observability/ops/healthhttp/tracker.go) |
| [log](../../log/module.yaml) | [公共构造](../../log/logger.go)、[文本写入](../../log/glog_writer.go)、[轮换](../../log/glog_rotation.go)、[JSON 分流](../../log/json.go) | [访问日志](../../middleware/accesslog/accesslog.go)、[输出与归档复验](../../log/glog_test.go) |
| [metrics](../../metrics/module.yaml) | [基础记录和快照](../../metrics/collector.go)、[聚合](../../metrics/multi.go) | [HTTP 适配](../../middleware/httpmetrics/http_metrics.go)、[统计竞争回归](../../metrics/concurrency_regression_test.go) |
| 根门面 | [New、配置与 Param](../../plumego.go) | [公开门面测试](../../plumego_test.go) |

边界与稳定 API 快照检查通过。健康 Tracker 的编排与历史问题在扩展宿主内处理。

## 状态与风险

| 路径 | 当前控制点 | 实施约束 |
|---|---|---|
| 健康报告 | 批次终态与组件缓存分别发布，报告读取组件缓存 | 修复异常终态时只发布仍有效的检查结果。手工就绪与实际组件状态分别验收。 |
| gzip | 缓冲状态先于底层提交，Flush 属可选能力 | 不支持的底层 writer 应保有 fallback 能力。已开始的流保持所有权。 |
| 历史 | 列表追加顺序、时间统计和小页各有用途 | 按时间找极值，列表继续按追加顺序。页只复制目标记录，仍返回完整匹配数。 |
| KV | 真实目录的 sidecar 锁与存储提交属于不同阶段 | 平台上的别名锁、重试后 TTL 和所有权分别验证。 |
| 日志与指标 | 依据 active link、writer 和独立子快照判断所有权 | 只验证现有合同，遇到范围不明的内部轮换先取证。 |

## 第 9 节逐项证据

| 编号 | 核查项 | 证据与边界 |
|---|---|---|
| 01 | 所有权 | 核对九个稳定模块清单与 ops 清单。批次发布与报告来自 ops，健康模型留在 health，Flush 能力留在压缩适配。 |
| 02 | 依赖方向 | boundary-check 通过；抽查 core → router、middleware → contract/security。稳定层没有为健康宿主反向引入扩展。 |
| 03 | 根门面 | [plumego.go](../../plumego.go) 对应批准的六个便利导出。根包正常入口通过，405 策略透传单列补证。 |
| 04 | 标准库 | [主模块](../../go.mod) 未增加第三方库；[依赖规则](../../specs/dependency-rules.yaml) 与内部传输助手调用方通过基线检查。 |
| 05 | 显式装配 | [标准服务](../../reference/standard-service/internal/app/app.go) 显式传入依赖；[core](../../core/dependencies.go) 选择 logger。复审路径未发现服务定位注册。 |
| 06 | 稳定契约 | 九根 API 快照匹配。历史时间统计、奇数趋势、Flush 前后状态变化仍须按候审验收单独修复。 |
| 07 | 兼容边界 | 严格 deprecation inventory 通过。[KV Options](../../store/kv/options.go) 和 [DB QueryRow](../../store/db/sql.go) 的保留入口沿既有跟踪。 |
| 08 | 可逆范围 | 候审卡单个主要模块，最多五条文件路径和三个验证命令。报告问题的状态修复与手工就绪兼容用例分卡。 |
| 09 | 命名可读 | [HealthTrend 字段](../../x/observability/ops/healthhttp/tracker.go) 的 recent/older 与计算分母错位；[HTTP 指标](../../metrics/collector.go) 的原始时长与秒单位分开。 |
| 10 | 主执行顺序 | [历史分页](../../x/observability/ops/healthhttp/tracker.go) 当前先构造匹配集再取页。计划单次扫描记总数与小页，保留排序和筛选。 |
| 11 | 阶段职责 | [core](../../core/http_handler.go) 区分准备 handler、构造 server 和发布；[压缩](../../middleware/compression/gzip.go) 区分缓存状态与提交，显式 Flush 缺少能力前置判断。 |
| 12 | 辅助函数 | [响应提交](../../contract/response.go) 共用 body write helper；[历史条件](../../x/observability/ops/healthhttp/tracker.go) 保留同一匹配规则并验证页宽差异。 |
| 13 | 接口粒度 | [HTTPObserver](../../metrics/collector.go)、[ComponentChecker](../../health/core.go)、[TraceSpan](../../middleware/internal/telemetry/helpers.go) 各自服务实际消费点。建议变更使用既有公开签名。 |
| 14 | 配置语义 | [历史分页](../../x/observability/ops/healthhttp/tracker.go) 中 50、100 和负 offset 语义已查实，模块说明待精确同步。 |
| 15 | 构造失败 | [KV](../../store/kv/kv.go) 返回归一化和锁错误，[SQL OpenWith](../../store/db/sql.go) 负责部分池。额外别名与 Rows 资源通过独立卡核对。 |
| 16 | 约束说明 | [健康宿主说明](../../docs/modules/x/observability/ops/README.md) 覆盖格式与非法字面值，缺少分页整数范围。日志平局、缓存过期计费待确认。 |
| 17 | HTTP 接口 | [router](../../router/dispatch.go)、[middleware 包装](../../middleware/middleware.go) 保持 net/http 形状；标准 FlushError 层叠路径另验。 |
| 18 | 输入来源 | [历史](../../x/observability/ops/healthhttp/history.go) 显式读 query，[RequestID](../../contract/request_id.go) 限制 UTF-8 字节，[bodylimit](../../middleware/bodylimit/body_limit.go) 使用请求体。 |
| 19 | 注册错误 | [标准装配路由](../../reference/standard-service/internal/app/routes.go) 方法、路径、处理器并列；同名失败后热路径与选项重入另用合成例核对。 |
| 20 | middleware 顺序 | [CORS](../../middleware/cors/cors.go) 根据请求分支透传或终止， [recovery](../../middleware/recovery/recover.go) 按 writer 状态收尾。层叠 request ID 与原响应归属另验。 |
| 21 | 规范 JSON | [contract](../../contract/response.go) 标准 JSON 和错误外壳；[健康表示](../../x/observability/ops/healthhttp/helpers.go) 延续扩展维护的健康资源响应，编码失败走规范错误。 |
| 22 | 分类 | [SQL](../../store/db/sql.go) 合并错误原因，[gzip](../../middleware/compression/gzip.go) 返回 ErrNotSupported 但仍提前提交的个案已复现。取消健康批次理由不明确。 |
| 23 | 提交归属 | 明确尚未 Flush 的压缩包装出现副作用。另保留 HEAD Reader 中途失败、超时回放替换及恢复后不二次提交的负例。 |
| 24 | writer 能力 | [组合测试](../../middleware/conformance/response_writer_contract_test.go) 与中间件基线 race 通过。新任务分别探明已提交、完全不支持和动态返回错误三种能力。 |
| 25 | 敏感数据 | [Tracker](../../x/observability/ops/healthhttp/tracker.go) 的组件失败记录使用固定诊断；[日志](../../log/json.go) 的字段编码回退为固定值。未读取真实凭据或生产日志。 |
| 26 | 取消传播 | [Manager 重试](../../x/observability/ops/healthhttp/manager.go) 保留 Context 原因；Readiness 则仍返回空的失败组件原因。锁内等待和 trace 派生 Context 的交叠单列。 |
| 27 | Context 身份 | [请求标识](../../contract/request_id.go)、[路由载体](../../contract/context_core.go)、[追踪](../../contract/trace.go) 各自使用私有键。多层标识来源和可选父指针另补。 |
| 28 | 阶段状态 | [LifecycleGroup](../../core/lifecycle_group.go) start/stop 转换及 pending 标志已有测试；最后一次启动回调触发取消、重复停止失败顺序另证。 |
| 29 | 资源所有权 | [KV 部分失败](../../store/kv/kv.go) 关闭锁，[DB scan](../../store/db/sql.go) 清理 Rows，[静态文件](../../router/static.go) 关闭文件。别名锁定与两个清理错误待定向验证。 |
| 30 | 清理错误 | [DB 错误合并](../../store/db/sql.go) 和 [KV 目录同步](../../store/kv/persistence.go) 继续记录主因和清理失败。原生跨平台文件系统差异本轮未验证。 |
| 31 | 发布边界 | Manager 把聚合结果和 readiness 标为失败，报告仍可能读到上一份组件缓存。新增版本保护要求聚焦已尝试的异常项。 |
| 32 | 同步不变量 | 稳定范围 race 通过。探测批次与单组件并发结果、并发准备诊断和 Clear 后的快照属单独契约验证。 |
| 33 | 回调和锁 | [路由选项](../../router/registration.go) 先于锁，[core 告警](../../core/http_handler.go) 后于发布。嵌套注册重入和附加诊断发布次数待证。 |
| 34 | 可变所有权 | [健康模型](../../health/core.go) 的详情按约定浅复制、依赖列表独立；[历史统计](../../x/observability/ops/healthhttp/tracker.go) 的极值副本和小页应保持调用方所有。 |
| 35 | 资源上界 | [缓存](../../store/cache/memory.go) 载荷按追踪值统计；[历史分页](../../x/observability/ops/healthhttp/tracker.go) 的小页临时分配仍随匹配集合增加。 |
| 36 | core | 现有启动、关停和回调回归通过。诊断发布、最终 Start 取消、逆序重试及配置转发另证。 |
| 37 | router | [完整匹配](../../router/matcher.go) 和 [缓存](../../router/cache.go) 与 baseline race 一起核对，重复名称、通配双回退、静态文件矩阵另证。 |
| 38 | contract | [响应规范](../../contract/response.go) 和 [错误链](../../contract/errors.go) 跑现有基线；短写、失败详情双错误及 route state 溢出联测仍列卡。 |
| 39 | middleware | 组合测试基线通过，压缩能力缺失的显式状态路径已复现，其他 wrapper 终态按正常和异常分支继续核对。 |
| 40 | security | [凭据比较](../../security/authn/token.go) 与 [JWT 验证](../../security/jwt/verify.go) 入口和现有负例已查；只安排合成策略与本机存储夹具。生产信任配置未取证。 |
| 41 | store | [KV](../../store/kv/kv.go)、[cache](../../store/cache/memory.go)、[DB](../../store/db/sql.go) 现有 race 通过；目录别名、发布失败和 TTL 组合另证。 |
| 42 | health | 健康模型定义及现有测试通过。宿主层确认报告与缓存分歧、就绪原因、奇数窗口和历史时间边界。 |
| 43 | log | 已查 [文本提交](../../log/glog_writer.go) 与 [JSON 归一化](../../log/json.go)。文件轮换开关、相同修改时间与字段回调次数保留补证。 |
| 44 | metrics | [原始 HTTP Record](../../metrics/collector.go) 同时存秒与精确时长；复用现有并发用例，补证不同名称的 epoch 和健康宿主趋势逻辑。 |
| 45 | 定向验证 | 五项使用公开入口的临时本机测试，另对分页做基准；实施时在 owner 包加入失败到通过的用例。 |
| 46 | 负例 | 本轮观察到 Goexit、取消、未支持能力和乱序时间。短写与失败后可重试条件在卡片中另行核查。 |
| 47 | 并发证据 | 本轮复现使用同步调用与本机合成依赖；未来并发卡用通道或条件等待，明确不只依赖固定睡眠。 |
| 48 | 性能测量 | limit=1 时比较 100 与 10000 条历史的三次分配。后续要用相同负载检验语义等价和页宽上界。 |
| 49 | 格式与 lint | make fmt-check 与 make lint 通过，lint 0 项。审查阶段未执行原地格式化。 |
| 50 | 门禁选择 | 稳定范围 race、vet、边界、API、coverage 已运行；新增 Markdown 走 docs_only 差异配置。 |
| 51 | 覆盖率 | 稳定模块总语句覆盖率 96.8%。新发现不从覆盖率推导，取消、顺序和底层能力使用单独证据。 |
| 52 | 记录 | 本报告记录基线代码、53 张卡和实际命令，文档行为同步由对应实现卡执行。 |

## 本机复现

| 临时测试 | 当前观察 |
|---|---|
| `TestReportUsesHealthyCacheAfterAbortedBatch` | 聚合 unhealthy，就绪 false，报告仍返回 HTTP 200 和 healthy。 |
| `TestOddTrendWindowUsesOtherHalfSize` | 3 条均成功，得到 improving 与 1.5。 |
| `TestHistoryStatsUseAppendOrderForTimestamps` | 3 条乱序时间，OldestEntry 晚于 NewestEntry，TimeSpan 为 -2 秒。 |
| `TestCanceledEmptyBatchLosesReadinessReason` | 批次消息为 context canceled，就绪 Reason 为 components failed: []。 |
| `TestGzipUnsupportedExplicitFlushCommitsAndDisablesEncoding` | ErrNotSupported 已返回，但 202 已在正文前提交，后续响应无 gzip 编码。 |

分页基准使用内置 Manager 和 Tracker 产生无外部依赖的合成历史，公开 QueryHealthHistory 每次取首页一条。Go 1.26.0、darwin/arm64、Apple M1 Pro。三次采样中位数如下。

| 匹配数 | ns/op | B/op | allocs/op |
|---:|---:|---:|---:|
| 100 | 2927 | 24432 | 8 |
| 10000 | 872575 | 3579767 | 19 |

## 验证记录

| 命令 | 结果 |
|---|---|
| `git show --format= --check HEAD` | 通过，基线提交为 2b94cf86。 |
| `make boundary-check api-check fmt-check lint stable-coverage` | 通过。九个稳定根快照匹配，lint 0 项，覆盖率 96.8%。 |
| `go test -race -timeout 60s ./core ./router ./contract ./middleware/... ./security/... ./store/... ./health ./log ./metrics ./x/observability/ops/healthhttp .` | 通过。 |
| 对同一包集合运行 `go vet` | 通过。 |
| `go test -race -timeout 20s -v /tmp/plumego-2b94-probe_test.go` | 五项当前行为均得到本机验证。临时桩位于工作区外。 |
| `go test -run '^$' -bench '^BenchmarkHistoryFirstSmallPage$' -benchmem -benchtime=100ms -count=3 /tmp/plumego-2b94-probe_test.go` | 每个场景三次采样通过，明细见上表。 |
| `make validate-diff-dry`、`make validate-diff` | 通过，选择 `docs_only`，无额外执行命令。 |
| 逐卡 `make check-card C=review-2b94cf86/<卡名>` 并检查编号、源码行与链接 | 53 张全部通过，核对 52 个检查号与 300 条链接。工作区仅新增 55 个 Markdown 文件。 |

本轮取证限于列出的代码路径、合成依赖及当前平台。参考模块全量测试、CLI 全量测试、网站构建、原生跨平台和真实生产部署未重跑。
