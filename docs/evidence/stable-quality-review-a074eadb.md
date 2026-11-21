# 稳定模块第 9 节质量复审

基线提交为 `a074eadb`（落实稳定模块复审修复与边界验证）。范围覆盖九个稳定模块和根门面，并追踪健康宿主及标准服务作为代表性调用链。

本文保留修复前的证据，基线可用 `git show a074eadb:<路径>` 查阅。源码链接已对齐当前入口。 [实施与验证记录](../../tasks/cards/review-a074eadb/README.md)

## P0–P3 发现

本轮记录 **9 项有证据的发现**，其中 6 项使用公开入口和本机桩复现。其余 42 张是待验证的契约或性能任务。本轮未确认 P0。生产配置、第三方实现的实际部署及原生平台矩阵未取证。

| 优先级 | 已复现 | 源码确认 | 补证任务 |
|---|---:|---:|---:|
| P0 | 0 | 0 | 0 |
| P1 | 2 | 0 | 0 |
| P2 | 4 | 2 | 39 |
| P3 | 0 | 1 | 3 |

证据状态标在每项后。源码确认表示能直接从当前实现追溯该路径，现场影响仍取决于列出的触发条件。

### P1

- **1797 健康组件命名回调重入时注册互等**（已复现，检查号 5,29,33,42,47）
  - 注册持 manager 写锁调用注入 checker.Name。合成 Name 同步读取 GetConfig 时注册不能完成，其他需要该锁的操作也会受阻。 [x/observability/ops/healthhttp/manager.go:158](../../x/observability/ops/healthhttp/manager.go#L158)
  - 最小方案。在锁外取得并校验名字，获取锁后重新核对关闭与注册状态再发布。
  - 验收。回调可读现有配置且注册完成，关闭期间不发布组件，空名字仍拒绝。 [任务卡](../../tasks/cards/done/1797-register-name-callback-lock.md)
- **1798 健康聚合把异常退出的 worker 当成成功**（已复现，检查号 23,28,29,33,42,46）
  - 组件已有健康快照时，后续 Check 调用 Goexit。worker 会归还配额并完成 WaitGroup，但未报告结果，聚合实际返回 healthy 且 Ready=true。后台回调 panic 也缺少本地隔离，后者仅由源码确认。 [x/observability/ops/healthhttp/manager.go:516](../../x/observability/ops/healthhttp/manager.go#L516)
  - 最小方案。每个已调度组件必须产生一次终态。异常退出记录固定失败，聚合按失败或结果缺失判未就绪，保留版本护栏。
  - 验收。预热后模拟异常退出，旧快照不得令聚合成功；其余 worker 正常结束，后续检查能恢复。 [任务卡](../../tasks/cards/done/1798-aggregate-worker-abnormal-result.md)

### P2

- **1799 健康重试在等待期间丢失取消原因**（已复现，检查号 22,26,42,46）
  - 合成检查失败后进入重试等待，取消 Context 时返回上一次 checker 错误。若取消在检查返回时已可见，则同一函数返回 Context 错误。 [x/observability/ops/healthhttp/manager.go:484](../../x/observability/ops/healthhttp/manager.go#L484)
  - 最小方案。取消中断重试时返回 ctx.Err，尝试次数保持已执行次数。
  - 验收。同一个合成依赖对照检查中取消、退避中取消和普通重试，验证错误链与记录次数。 [任务卡](../../tasks/cards/done/1799-retry-delay-context-error.md)
- **1800 最大 RetryCount 被溢出成单次检查**（已复现，检查号 14,22,35,42）
  - 构造接受最大 int。受控检查第一次失败、第二次本可成功，实际仅调用一次，配置被静默改变。 [x/observability/ops/healthhttp/manager.go:464](../../x/observability/ops/healthhttp/manager.go#L464)
  - 最小方案。定义可表达的总尝试次数，在公共配置验证边界拒绝不可表达值或以不溢出的边界计算。
  - 验收。构造和 SetConfig 处理一致，普通 0、1 重试仍符合约定，负值继续拒绝。 [任务卡](../../tasks/cards/done/1800-retry-count-upper-bound.md)
- **1801 HEAD handler 异常退出时生成隐式成功状态**（已复现，检查号 23,24,37,46）
  - 未写头的同一 handler 主动结束 goroutine，GET 不提交，HEAD 外层 defer 提交一次 200。panic 的现有分支仅提交已有逻辑状态。 [router/dispatch.go:170](../../router/dispatch.go#L170)
  - 最小方案。记录下游是否正常返回。异常退出时按已有逻辑状态处理，避免制造隐式成功。
  - 验收。对照 GET、HEAD、Goexit、普通空返回和显式状态，核对最终提交次数。 [任务卡](../../tasks/cards/done/1801-head-abnormal-exit-commit.md)
- **1802 历史导出忽略无法解析的时间参数**（已复现，检查号 18,21,22,38,46）
  - 显式 start_time 非法时公开历史导出入口返回 200，解析放弃过滤。end_time 使用相同结构。调用方可能接收到超出原时间范围的结果。 [x/observability/ops/healthhttp/history.go:75](../../x/observability/ops/healthhttp/history.go#L75)
  - 最小方案。两个字段复用既有参数错误路径，校验合法范围并保持缺省查询。
  - 验收。无效起止时间均为固定 400，无原输入回显，合法窗口和缺省查询保持。 [任务卡](../../tasks/cards/done/1802-history-invalid-time-filter.md)
- **1803 健康聚合并发限制仍为每组件创建 goroutine**（源码确认，检查号 26,29,35,42,48）
  - 每个注册项都创建 worker，信号量只限制进入检查的数量。大量本地配置的组件会产生随注册数增长的等待 goroutine，取消也要等待该批清空。 [x/observability/ops/healthhttp/manager.go:516](../../x/observability/ops/healthhttp/manager.go#L516)
  - 最小方案。用固定数量 worker 消费组件序列，取消时停止未开始项，明确失败与缺失计数。
  - 验收。以同步屏障核对活动 worker 上界、取消及版本不变量，并记录大组件集的分配。 [任务卡](../../tasks/cards/done/1803-aggregate-worker-count-bound.md)
- **1804 共享日志目录的保留策略缺少其他实例的活动文件信息**（源码确认，检查号 16,29,31,43）
  - 排除的活动文件仅来自本实例，候选按程序名、等级和时间归组。另一同名实例共用目录时，其文件可进入归档删除候选。未验证多进程部署。 [log/glog_rotation.go:250](../../log/glog_rotation.go#L250)
  - 最小方案。先明确目录所有权和兼容范围；需支持共享时，按可验证的实例归属隔离活动文件和清理对象。
  - 验收。合成互异文件组和活动集，验证一方轮换不会移除另一方活动文件，同时保持自己的备份配额。 [任务卡](../../tasks/cards/done/1804-retention-instance-ownership.md)

### P3

- **1805 健康间隔配置缺少未启用调度的说明**（源码确认，检查号 6,14,16,42,52）
  - Config 暴露 HealthCheckInterval，Manager 中没有消费此字段的周期调度。设置该字段不等于自动检查，容易误判缓存新鲜度。 [x/observability/ops/healthhttp/manager.go:29](../../x/observability/ops/healthhttp/manager.go#L29)
  - 最小方案。核对调用方后记录该字段的兼容用途及检查触发方。
  - 验收。给出实际检查入口和配置所有者，保持现有序列化字段。 [任务卡](../../tasks/cards/done/1805-manager-interval-config-doc.md)

## 执行顺序

先修复组件注册的回调锁边界（1797）及聚合异常终态（1798），随后核对重试取消（1799）与次数边界（1800）。HEAD 的异常终态（1801）可独立执行。聚合 worker 资源上界（1803）以异常终态回归为依赖。各卡的精确文件范围和命令见 [实施索引](../../tasks/cards/review-a074eadb/README.md)。

## 模块取证

使用 `stable_root_boundary_review` 路由与 review 上下文包。读取九份稳定模块清单及 [扩展清单](../../x/observability/module.yaml)、[宿主清单](../../x/observability/ops/module.yaml)。根门面无独立清单。本轮写入报告和候审目录，源码、依赖、公开 API、运行行为、安全配置及已有任务归档均无变更。既有 Windows 与 Plan 9 原生锁验证见 [1615](../../tasks/cards/blocked/1615-kv-os-lock-platform.md)。

| 模块 | 公共入口和内部路径 | 代表调用方及核验 |
|---|---|---|
| [core](../../core/module.yaml) | [Prepare / Shutdown](../../core/lifecycle.go)、[handler 发布](../../core/http_handler.go)、[LifecycleGroup](../../core/lifecycle_group.go) | [标准服务装配](../../reference/standard-service/internal/app/app.go)、[阶段回归](../../core/lifecycle_test.go) |
| [router](../../router/module.yaml) | [注册](../../router/registration.go)、[HEAD 与分派](../../router/dispatch.go)、[静态文件](../../router/static.go)、[URL](../../router/metadata.go) | [core 路由](../../core/routing.go)、[路由观察](../../router/route_observer_test.go) |
| [contract](../../contract/module.yaml) | [WriteResponse / WriteError](../../contract/response.go)、[分类和拷贝](../../contract/errors.go)、[追踪](../../contract/trace.go) | [标准路由](../../reference/standard-service/internal/app/routes.go)、[响应测试](../../contract/response_test.go) |
| [middleware](../../middleware/module.yaml) | [Chain](../../middleware/middleware.go)、[正文限制](../../middleware/bodylimit/body_limit.go)、[并发限制](../../middleware/concurrencylimit/concurrency_limit.go)、[观察收尾](../../middleware/internal/telemetry/helpers.go) | [writer 组合](../../middleware/conformance/response_writer_contract_test.go)、[超时回归](../../middleware/timeout/timeout_regression_test.go) |
| [security](../../security/module.yaml) | [验证](../../security/jwt/verify.go)、[内存密钥存储](../../security/jwt/keystore_memory.go)、[限流](../../security/abuse/limiter.go)、[响应策略](../../security/headers/headers.go) | [认证中间件](../../middleware/auth/auth.go)、[限流测试](../../security/abuse/limiter_test.go) |
| [store](../../store/module.yaml) | [缓存](../../store/cache/memory.go)、[KV 加载](../../store/kv/persistence.go)、[SQL](../../store/db/sql.go)、[文件元数据](../../store/file/types.go)、[幂等](../../store/idempotency/store.go) | [KV 状态](../../store/kv/state_regression_test.go)、[缓存状态](../../store/cache/state_regression_test.go)、[数据库桩](../../store/db/sql_test.go) |
| [health](../../health/module.yaml) | [模型状态与 Clone](../../health/core.go)、[就绪](../../health/readiness.go) | [单组件 HTTP](../../x/observability/ops/healthhttp/handlers.go)、[内置 Manager](../../x/observability/ops/healthhttp/manager.go)、[模型回归](../../health/json_contract_test.go) |
| [log](../../log/module.yaml) | [NewLogger](../../log/logger.go)、[初始化与轮换](../../log/glog_rotation.go)、[JSON](../../log/json.go) | [访问日志](../../middleware/accesslog/accesslog.go)、[文件测试](../../log/glog_test.go) |
| [metrics](../../metrics/module.yaml) | [Record / Clear](../../metrics/collector.go)、[聚合](../../metrics/multi.go) | [HTTP 适配](../../middleware/httpmetrics/http_metrics.go)、[并发统计](../../metrics/concurrency_regression_test.go) |
| 根门面 | [构造、别名和 Param](../../plumego.go) | [hello 示例](../../examples/hello/main.go)、[公开门面测试](../../plumego_test.go) |

## 关键状态与所有权

| 路径 | 发布或终态 | 待修复或验证 |
|---|---|---|
| 组件注册 | 检查名字并发布组件与初始状态 | 注入 Name 目前在写锁内执行。锁外检查后需重新核对关闭，再发布。 |
| 聚合检查 | 读取组件序列，等待 worker 并形成结果，按版本发布 | 退出可以只递减 WaitGroup 而没有结果，旧健康状态仍参与聚合。每个预期项应有终态。 |
| 退避取消 | 检查失败进入等待，后续尝试可重新执行 | 取消终止等待时旧错误覆盖取消分类。尝试次数按已实际执行的次数记录。 |
| HEAD | 路由可先记录逻辑状态，defer 完成 HTTP 提交 | 异常退出且没有选定状态时实际提交 200。正常空返回仍需要原约定。 |
| 日志归档 | 清理排除当前 logger 已知活动目标 | 其他同名实例的活动文件没有列入本实例集合，目录共享边界待核对。 |

## 第 9 节逐项证据

| 编号 | 核验项 | 证据与限制 |
|---|---|---|
| 01 | 模块归属 | 阅读九根及健康宿主清单；健康模型归 health，检查调度、查询和 HTTP 终态归 healthhttp。 |
| 02 | 依赖方向 | 边界门禁通过；跟踪 core→router、healthhttp→health/contract、HTTP metrics→metrics 三条正常依赖。 |
| 03 | 薄门面 | [根门面](../../plumego.go) 维持构造、别名和 Param。集中验证批准的导出项，父子参数竞争留补证。 |
| 04 | 标准库与内部依赖 | [go.mod](../../go.mod) 无第三方依赖。内部 writer 工具按照既有 [调用边界](../../specs/dependency-rules.yaml) 使用。 |
| 05 | 显式构造 | [标准服务装配](../../reference/standard-service/internal/app/app.go) 显式注入；Manager 的注册依赖可见，命名回调发生锁持有期间（1797）。 |
| 06 | 稳定契约 | 九根 API 快照一致。HEAD 异常终态、历史导出宽松解析和日志共享目录涉及现有可观察行为，分卡核对。 |
| 07 | 兼容层处置 | [废弃清单](../../specs/deprecation-inventory.yaml) 严格检查通过。健康间隔字段现有序列化和用途由宿主兼容卡核对。 |
| 08 | 变更可逆 | 候审卡限制单主模块、最多五文件和三条命令。聚合 worker 容量变更依赖异常结果修复验收。 |
| 09 | 命名意图 | [检查尝试](../../x/observability/ops/healthhttp/manager.go) 区分重试次数与总尝试数，但极端值计算有偏差（1800）。 |
| 10 | 主流程 | [HEAD 分派](../../router/dispatch.go) 的提交由 defer 收口。正常返回与无 panic 的异常退出尚未区分（1801）。 |
| 11 | 职责与阶段 | [健康管理器](../../x/observability/ops/healthhttp/manager.go) 分别处理注册、检查和聚合；异常终止不进入结果发布（1798）。 |
| 12 | 助手封装 | [历史时间解析](../../x/observability/ops/healthhttp/history.go) 起止字段采用相同吞错方式，已有结构化参数错误入口可共用。 |
| 13 | 小接口 | [health 检查接口](../../health/core.go) 仅 Name/Check；metrics Recorder 与 StatsReader 保持分工。错误 As 外部回调留兼容补证。 |
| 14 | 配置语义 | RetryCount 边界未限制可表示总次数，间隔字段未驱动宿主周期调度；二者有独立卡。 |
| 15 | 构造失败 | [稳定 Chain](../../middleware/middleware.go) 处理 nil builder 和其无效返回；空最终 handler 在不同组合下留补证。 |
| 16 | 约束文档 | 查阅日志保留范围和健康间隔；前者缺少共享目录的所有权信息，后者缺少当前触发方说明。 |
| 17 | net/http 兼容 | [Router](../../router/dispatch.go)、[Chain](../../middleware/middleware.go) 与 [healthhttp handler](../../x/observability/ops/healthhttp/handlers.go) 使用标准入口。 |
| 18 | 输入来源 | 历史时间从 query 读取。显式非法时间被忽略，公共入口观测到 200；授权及 URL 卡使用本机输入。 |
| 19 | 可搜索注册 | [标准路由装配](../../reference/standard-service/internal/app/routes.go) 集中列明方法、路径、处理器和注册失败；已冻结 RouteGroup 待补证。 |
| 20 | 中间件控制流 | bodylimit、timeout、CORS 现有回归通过；补证聚焦部分响应后的 Flush 和异常退出与取消交叠。 |
| 21 | 规范 JSON | [contract writer](../../contract/response.go) 是规范路径；健康历史已具有参数拒绝 writer，时间字段尚未接入。 |
| 22 | 错误分类 | 健康 retry 退避中取消丢失 Context 原因；DB 事务提交与 Ping 取消的状态另有验证卡。 |
| 23 | HTTP 提交边界 | HEAD 的异常退出额外提交成功已复现；已选头和异常结果的组合须保留已有终态。 |
| 24 | writer 能力 | [组合 writer](../../middleware/conformance/response_writer_contract_test.go) 随 race 执行；tracing 短写、HEAD 头快照、正文限制 Flush 留补证。 |
| 25 | 敏感数据 | 检查 [URL 校验](../../security/input/input.go) 与 [日志写入](../../log/glog_writer.go) 的固定诊断约束。生产凭据和运行日志采样未验证。 |
| 26 | 取消传播 | 检查前及返回后验证 Context，但健康退避中止返回旧错误（1799）。排队中聚合 worker 和 DB 调用另证。 |
| 27 | 上下文范围 | [RouteState](../../contract/context_core.go) 与父元数据分别存储。核对 nil 与父参数的读取优先级。 |
| 28 | 状态转换 | 聚合健康已有批次版本。worker Goexit 时缺终态；core 构建中关停及缓存已关闭又过期等交叠待补证。 |
| 29 | 资源归属 | 检查健康 goroutine 和信号量，当前等待者随注册数增长；日志活动文件集按本实例生成。 |
| 30 | 清理错误 | [日志轮换](../../log/glog_rotation.go) 已分离链接发布和 writer；链接失败与随后归档清理需要组合验证。 |
| 31 | 提交和重试 | 活动日志保留依赖本实例快照。DB 提交错误、KV 重开淘汰、核心构建失败后的关停重试按卡验收。 |
| 32 | 并发不变量 | 稳定范围 race 通过。worker 未报告、聚合先后与缓存末态需由状态断言补证。 |
| 33 | 回调锁边界 | Manager 的 Name 回调重入锁已复现。worker 的 panic 隔离缺失使用静态源码确认。 |
| 34 | 可变数据归属 | 查阅 [错误详情](../../contract/errors.go)、[健康副本](../../health/core.go) 和 [文件副本](../../store/file/types.go)；存储和错误多快照路径按卡验证。 |
| 35 | 容量 | 源码确认聚合为每组件启动 goroutine。KeyStore 的小数组切换与高基数指标读分配待专项基准。 |
| 36 | core 生命周期 | [生命周期回归](../../core/lifecycle_test.go) 已运行；核心构建与 Shutdown、组快照和取消原因链另有补证。 |
| 37 | router 优先级 | [缓存、路由和 HEAD](../../router/router_contract_test.go) 随 race 执行；本轮复现 HEAD 异常退出提交错误。 |
| 38 | contract 传输 | [响应及错误](../../contract/response_test.go) 的基线测试通过。错误构建器快照、旁路编码和 As 回调需定向补证。 |
| 39 | middleware 组合 | 当前相邻 wrapper 通过 race。计划验证无正文指标、已编码 debug 旁路与 tracing 结束参数。 |
| 40 | security 拒绝 | [JWT](../../security/jwt/verify.go) 现有验证和发布矩阵已执行，静态凭据采用 timing-safe 原语。生产策略不在本机范围。 |
| 41 | store 一致性 | [KV 状态](../../store/kv/state_regression_test.go) 与 [缓存](../../store/cache/state_regression_test.go) 已运行，持久重开时间平局和终态访问顺序另证。 |
| 42 | health 状态 | [检查与聚合](../../x/observability/ops/healthhttp/manager.go) 已观测到旧快照误报、重试取消和次数偏差。稳定模型及其现有线格式测试通过。 |
| 43 | log 完整性 | 日志基线测试通过。共享目录保留问题由源码确认，跨进程调度及原生重命名语义未验证。 |
| 44 | metrics 一致性 | [并发统计回归](../../metrics/concurrency_regression_test.go) 已运行。状态与显式 Error、空与非空聚合快照、读分配各留一张补证。 |
| 45 | 聚焦回归 | 六项观察使用公开入口的本机桩。卡片包含狭窄文件范围、验收与可执行命令。 |
| 46 | 负向路径 | 已取证回调异常退出、取消和非法查询输入；未测试真实服务、生产凭据或外部代理。 |
| 47 | 确定性并发 | 退避取消使用 synctest，注册局部阻塞用回调到达信号和有界等待。在途扫描及发布顺序有具体同步方案。 |
| 48 | 性能 | 未得出本轮性能变化结论。worker 上界、密集有效 KeyStore 和指标快照成本有基准计划。 |
| 49 | 格式与 lint | fmt-check 与固定 lint 版本通过。Markdown 尾部空白及源码定位单独验证。 |
| 50 | Diff 门禁 | 对已提交基线执行稳定模块门禁。报告和卡片按 docs_only 选择验证。 |
| 51 | 覆盖率 | 稳定根实测 96.5%。缺失 worker 终态、退避取消、原生平台边界按影响独立记证。 |
| 52 | 文档与交付 | 记录基线、具体发现、源码位置、最小方案与限制。新审查材料留工作区。 |

## 复现记录

| 本机测试 | 观察结果 |
|---|---|
| `TestRegisterNameCallbackReentry` | Name 同步调用同一 manager 的 GetConfig。回调到达后，注册在本机有界观察窗口内未返回。 |
| `TestAggregateAbnormalWorkerExit` | 先取得健康快照，随后一个 worker 在检查中结束 goroutine。公开聚合返回 healthy，就绪状态为 true。 |
| `TestRetryDelayCancellationCause` | 合成时钟确定普通失败进入退避。取消后错误仍满足 checker 原因分类，无法匹配 context.Canceled。 |
| `TestRetryCountOverflow` | 构造接受最大 int 的重试计数。首次失败、第二次可成功，实际只执行一次。 |
| `TestHeadAbnormalExitCommitsImplicitSuccess` | 在同一简单路由使用合成 Goexit，记录器观测 GET 零次提交、HEAD 一次 200 提交。 |
| `TestHistoryInvalidTimestampAccepted` | 传入无法解析的时间字面值，公开历史导出仍返回 200。 |

## 验证记录

| 命令 | 结果 |
|---|---|
| `make boundary-check api-check fmt-check lint stable-coverage` | 通过，九根快照匹配，lint 0 项，稳定模块覆盖率 96.5%。 |
| `go test -race -timeout 90s ./core/... ./router/... ./contract/... ./middleware/... ./security/... ./store/... ./health/... ./log/... ./metrics/... ./x/observability/ops/healthhttp .` | 通过。 |
| 对同一范围执行 `go vet` | 通过。 |
| `go test -timeout 5s -v /tmp/plumego-a074-probe_test.go` | 前五项基线复现使用该命令。随后用 Go 文件覆盖将历史解析替换为基线实现，并用公开导出入口复验非法时间返回 200。 |
| `git show --format= --check HEAD` | 通过。 |
| `make validate-diff-dry`、`make validate-diff` | 通过。选择 `docs_only`，该配置没有额外命令。 |
| 逐张 `make check-card C=review-a074eadb/<卡名>`，另核对编号和链接 | 51 张全部通过。52 个检查号、270 条链接及源码行号均已验证。工作区只新增 53 个审查 Markdown 文件。 |

本轮未运行完整参考模块、CLI、网站构建、生产部署和原生跨平台矩阵。
