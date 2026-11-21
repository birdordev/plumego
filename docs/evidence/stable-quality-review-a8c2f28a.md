# 稳定模块第 9 节质量复审

基线为 `a8c2f28a`（完成稳定模块复审修复与回归验证）。范围为九个稳定模块和根门面，另检查健康宿主与标准服务的对应调用链。

以下记录基线观察，原代码可用 `git show a8c2f28a:<路径>` 查阅。源码链接已定位到当前对应入口。 [实施与验证记录](../../tasks/cards/review-a8c2f28a/README.md)

## P0–P3 发现

确认 **8 项问题**，其中 5 项由本机合成测试复现，3 项由源码确认。余下 44 项为定向补证。本轮未确认 P0。

| 优先级 | 已复现 | 源码确认 | 补证任务 |
|---|---:|---:|---:|
| P0 | 0 | 0 | 0 |
| P1 | 1 | 0 | 0 |
| P2 | 4 | 3 | 38 |
| P3 | 0 | 0 | 6 |

复现仅针对本机合成数据。源码确认描述可从调用路径直接推出的行为。补证项的验收结果尚待执行。

### P0

未确认。真实凭据、生产配置和部署可达性未取证。

### P1

- **1848 聚合未就绪时 Tracker 把未完成组件累计为成功**（已复现，检查号 28,31,32,42,46）
  - 组件先成功一次，下一批在 checker 回调中退出。Manager 返回 unhealthy，Tracker 从上一份健康快照又累计一次成功，成功数为 2，失败数为 0。指标应记录本批终态，跳过未启动的检查。 [x/observability/ops/healthhttp/tracker.go:282](../../x/observability/ops/healthhttp/tracker.go#L282)
  - 最小方案。在健康宿主内部传递本批组件终态，Tracker 只记录实际执行的结果。保留公开 Manager 接口，为外部实现注明采样限制。
  - 验收。围绕同一组件验证成功、异常、批次取消和重叠批次。聚合与组件计数一致，普通检查和 Manager 单独调用保留现有行为。 [任务卡](../../tasks/cards/done/1848-tracker-count-aborted-outcomes.md)

### P2

- **1849 压缩包装在 Goexit 路径隐式提交 200**（已复现，检查号 20,23,24,39,47）
  - handler 只修改 Header 后异常退出。gzip 分支提交一次 200，identity 分支没有提交。未选择状态的异常退出应让外层保留终态控制。 [middleware/compression/gzip.go:88](../../middleware/compression/gzip.go#L88)
  - 最小方案。显式记录下游是否正常返回。异常时沿用已有的未提交放弃、已提交资源收尾规则，保留 panic 传播。
  - 验收。本机 writer 对照 gzip、identity、正常空返回、panic、Goexit 和已开始的 gzip 流，分别核对提交次数、尾部和错误归属。 [任务卡](../../tasks/cards/done/1849-gzip-abnormal-header-commit.md)
- **1850 超出历史尾部的大 offset 错报还有下一页**（已复现，检查号 14,18,38,42）
  - 历史仅一项时，以平台最大 int 为 offset，页为空但 HasMore 为 true。offset 加 limit 溢出，客户端据此错误继续翻页。 [x/observability/ops/healthhttp/tracker.go:618](../../x/observability/ops/healthhttp/tracker.go#L618)
  - 最小方案。先比较 offset 与条目数。仅在有效区间内，用剩余条目数限制页宽后计算结束位置。
  - 验收。固定集合核对空集、末页、恰等于总数、最大 int 和普通下一页，保持 Total、Limit、Offset 字段语义。 [任务卡](../../tasks/cards/done/1850-history-offset-overflow.md)
- **1851 历史导出的无效分页参数悄悄回退到默认值**（已复现，检查号 14,18,21,22,42）
  - 分页字面值不合法时导出仍返回 200，回退为 limit 50、offset 0，调用方无法发现参数拼写错误。和同入口的时间、状态字段处理不一致。 [x/observability/ops/healthhttp/history.go:101](../../x/observability/ops/healthhttp/history.go#L101)
  - 最小方案。复用已有 invalidParamError，拒绝非整数或不可表达的分页字面值。保留既有空值和整数范围归一化，另记录如果需要改变整数范围的迁移决策。
  - 验收。分别验证 limit、offset 的缺省、合法、非整数和超出 int 范围，错误使用同一结构且不回显入参。 [任务卡](../../tasks/cards/done/1851-history-invalid-pagination.md)
- **1852 Tracker 平均耗时计算会把非负样本累积成负数**（已复现，检查号 31,35,42,44,46）
  - 注入两个同样的非负 Duration，乘加超出 int64 后平均值为负，落在最小和最大值之外。使用合成极值，生产触发阈值未测定。 [x/observability/ops/healthhttp/tracker.go:351](../../x/observability/ops/healthhttp/tracker.go#L351)
  - 最小方案。采用不会形成大中间积的在线平均，明确舍入精度并核对极值。
  - 验收。两次极值、交替大小及普通样本，平均值保持在 min/max 内且状态计数不受影响。 [任务卡](../../tasks/cards/done/1852-tracker-duration-average-overflow.md)
- **1853 每次健康聚合都追加历史且没有保留上限**（源码确认，检查号 14,29,35,42,48）
  - 运行期每次 CheckAllComponents 都追加一条，释放路径为显式 Reset 或实例生命周期结束。分页只限制返回页，历史保留量与总检查次数同阶。 [x/observability/ops/healthhttp/tracker.go:257](../../x/observability/ops/healthhttp/tracker.go#L257)
  - 最小方案。先核对宿主中使用历史的调用方。设计由宿主配置的条目上限或明确的保留策略，落实前记录默认值和兼容决策。
  - 验收。给出容量与留存量的本机测量，明确配置默认和调用方兼容，记录分页、统计及 Reset 的下一步验收。 [任务卡](../../tasks/cards/done/1853-tracker-history-retention.md)
- **1854 不支持的导出格式仍查询整个历史**（源码确认，检查号 10,18,35,42,48）
  - 处理器先过滤和构造分页结果，再进入 format 分支；无法导出的格式执行了全部查询工作。大历史会占用读锁并分配临时切片。 [x/observability/ops/healthhttp/history.go:52](../../x/observability/ops/healthhttp/history.go#L52)
  - 最小方案。保留现有参数错误优先级，完成有效格式判定后才访问历史。
  - 验收。组合不合法查询与格式验证错误顺序。对大小历史比较拒绝路径分配，JSON 和 CSV 输出不变。 [任务卡](../../tasks/cards/done/1854-history-format-check-before-read.md)
- **1898 Tracker 的可序列化统计保留原始依赖错误文本**（源码确认，检查号 22,25,38,42）
  - 单组件检查把底层错误交给统计记录，原文进入可序列化 RecentHistory。普通健康响应已使用固定消息。这里能直接确认未经归一化的数据流，实际部署及真实敏感值未取证。 [Tracker 记录](../../x/observability/ops/healthhttp/tracker.go#L361) · [指标输出](../../x/observability/ops/healthhttp/metrics.go#L11)
  - 最小方案。在写入可公开序列化的记录前转为固定诊断或已允许的分类，调用者直接收到的原因链保持原契约。
  - 验收。用无敏感数据的合成错误确认序列化字段只保留固定诊断，计数和调用者错误分类不变。 [任务卡](../../tasks/cards/done/1898-tracker-metrics-diagnostic-boundary.md)

### P3

本轮未确认此级别缺陷，相关补证见候审索引。

## 执行顺序

先执行 1848，接续 1849、1850 和 1851。1849 可独立验证。1854 在分页错误归一化后执行，以便固定错误优先级。Tracker 的留存策略先查调用方并测量，再确定兼容方案；敏感诊断路径的修复范围控制在扩展内。 [实施索引](../../tasks/cards/review-a8c2f28a/README.md)

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

## 关键状态与所有权

| 路径 | 当前控制点 | 实施约束 |
|---|---|---|
| 组件批次到统计 | Manager 依据执行和版本决定当前批终态，Tracker 随后读取快照 | 统计应区分本批执行、缺失和旧缓存。外部 Manager 兼容边界需保留。 |
| 历史导出 | 默认参数，过滤，分页，按格式写入 | 参数失败应在读历史前返回，响应错误不携带原始无效值。 |
| gzip | 包装器持有临时头，流开始后底层 writer 拥有最终状态 | 仅正常返回允许隐式成功。已开始的压缩流仍需结束资源。 |
| KV | 目录锁拥有单一修改者；快照替换后可见，目录同步另报告 | 重开和清理失败各有可重试边界，不能从单一错误推断提交状态。 |
| 日志和指标 | writer 串行拥有记录，指标合并顺序读取多个子快照 | 范围保持为输出与快照语义，不改变下游配置。 |

## 第 9 节逐项证据

| 编号 | 核查项 | 当前证据及验证边界 |
|---|---|---|
| 01 | 模块归属 | 九个稳定清单与 ops 宿主清单已检查。健康模型留在 health，探测和历史方案归 x/observability/ops。清单见模块表。 |
| 02 | 依赖方向 | `make boundary-check` 通过。抽查 core 导入 router，router 导入 contract，安全头由 middleware 调用 security。未见稳定根向扩展倒挂。 |
| 03 | 根门面 | [plumego.go](../../plumego.go) 保留六项便利导出；仓库 entrypoint 检查通过，nil 请求的薄转发另证。 |
| 04 | 依赖 | [go.mod](../../go.mod) 主模块沿用标准库。内部传输与遥测 helper 调用限制纳入边界检查，无依赖调整。 |
| 05 | 显式配置 | [标准服务装配](../../reference/standard-service/internal/app/app.go) 使用依赖注入， [logger 选择](../../core/dependencies.go) 从构造传入。两个 App 的配置与诊断隔离另证。 |
| 06 | 公开契约 | 九根 API 快照一致。运行契约另外核对压缩异常终态、分页、HEAD 写入及命名通配符，变更前保留调用方证据。 |
| 07 | 兼容层 | 严格 deprecation inventory 通过。[KV 配置](../../store/kv/options.go) 和 [SQL 单行入口](../../store/db/sql.go) 的兼容入口保留既有迁移边界。 |
| 08 | 可独立交付 | 缺陷修复以一个所有者为边界，聚合终态先于使用终态的 Tracker 补证，单卡至多五条文件路径。补证结果集中索引。 |
| 09 | 命名 | [MetricRecord](../../metrics/collector.go) 清楚区分值、时长和标签， [ReadinessStatus](../../health/readiness.go) 与存活区分。历史 HasMore 的实际结果例外已复现。 |
| 10 | 主流程 | [历史导出](../../x/observability/ops/healthhttp/history.go) 在选择格式前过滤历史。任务 1854 调整顺序并固定错误优先级。 |
| 11 | 责任阶段 | [core 准备](../../core/http_handler.go) 区分加载配置、冻结 handler、发布 server； [KV 持久化](../../store/kv/persistence.go) 区分替换与目录同步。 |
| 12 | 助手抽取 | [路由匹配](../../router/matcher.go) 共用完整分支判断， [历史参数](../../x/observability/ops/healthhttp/history.go) 的字段错误结构可复用；分页目前静默吞转换错误。 |
| 13 | 小接口 | [健康 checker](../../health/core.go)、[指标接口](../../metrics/collector.go)、[生命周期接口](../../core/lifecycle_group.go) 各自按消费能力划分。Tracker 修复优先沿用公开 Manager 签名。 |
| 14 | 配置 | [限流配置](../../security/abuse/limiter.go) 与 [缓存配置](../../store/cache/config.go) 有默认或拒绝入口。分页非法文本与普通缺省混同，已复现。 |
| 15 | 构造失败 | [OpenWith](../../store/db/sql.go) 校验并拥有部分句柄； [TLS 准备](../../core/http_handler.go) 返回加载错误；关闭错误和两种入口重试状态另证。 |
| 16 | 约束说明 | [日志目录及 writer](../../docs/modules/log/README.md) 已记录归属， [健康宿主](../../docs/modules/x/observability/ops/README.md) 已记录调度归属。Tracker 历史保留策略仍需明确。 |
| 17 | HTTP 兼容 | [router](../../router/dispatch.go) 与 [Chain](../../middleware/middleware.go) 保留 net/http 入口，实际 wrapper 能力另见 24。 |
| 18 | 输入来源 | 路径来自 [router](../../router/dispatch.go)，历史来自 [URL.Query](../../x/observability/ops/healthhttp/history.go)，正文来自 [bodylimit](../../middleware/bodylimit/body_limit.go)。分页拒绝路径已定位。 |
| 19 | 路由注册 | [标准服务路由](../../reference/standard-service/internal/app/routes.go) 显式处理注册错误； [选项与锁](../../router/registration.go) 的并发冻结时点另证。 |
| 20 | 中间件流程 | [并发限制](../../middleware/concurrencylimit/concurrency_limit.go) 在失败后短路， [压缩](../../middleware/compression/gzip.go) 依赖 defer 收尾。gzip 异常路径多一次提交。 |
| 21 | JSON 规范 | [contract](../../contract/response.go) 为规范响应路径。 [健康导出](../../x/observability/ops/healthhttp/history.go) 复用其中的错误路径，CSV 保留自己的媒体类型。 |
| 22 | 错误分类 | [DB](../../store/db/sql.go) 保留主错误和关闭原因；缓存与 KV 使用各自 sentinel。历史分页不经已有参数分类，任务 1851。 |
| 23 | 提交归属 | [HEAD](../../router/dispatch.go) 区分正常返回和异常退出。 [gzip](../../middleware/compression/gzip.go) 仍出现异常时隐式成功，任务 1849。 |
| 24 | writer 能力 | [组合回归](../../middleware/conformance/response_writer_contract_test.go) 已运行。信息头、压缩空尾字段及接管后计数保留定向矩阵。 |
| 25 | 诊断数据 | [组件 HTTP](../../x/observability/ops/healthhttp/handlers.go) 使用固定失败消息，而 [Tracker](../../x/observability/ops/healthhttp/tracker.go) 直接保存原始错误字符串。未读取部署日志或真实凭据。 |
| 26 | 取消 | [重试](../../x/observability/ops/healthhttp/manager.go) 返回退避中止原因。缓存写锁、SQL 末段和超时迟到回调的取消点仍需补证。 |
| 27 | Context 范围 | [contract](../../contract/context_core.go) 和 [authn](../../security/authn/authn.go) 使用私有键与访问器。未见该路径用于服务定位。 |
| 28 | 状态转换 | [LifecycleGroup](../../core/lifecycle_group.go) 明确 idle/start/ready/stop 和 pending 清理。Tracker 的 Reset 与在途批次归属仍无确定证据。 |
| 29 | 资源归属 | [日志轮换](../../log/glog_rotation.go) 保留活动链接目标，[DB Rows](../../store/db/sql.go) 关闭部分结果，Tracker 历史保留量无限增长。 |
| 30 | 清理错误 | [KV 目录同步](../../store/kv/persistence.go) 合并 Sync/Close， [SQL](../../store/db/sql.go) 合并主错误和清理。部分打开关闭双失败及平台原生行为另证。 |
| 31 | 提交与重试 | KV 替换后将内存视为已提交。健康 Manager 根据版本发布就绪，Tracker 新批次指标误用旧快照已复现。 |
| 32 | 整体同步 | [聚合终态](../../x/observability/ops/healthhttp/manager.go) 与 [Tracker 指标](../../x/observability/ops/healthhttp/tracker.go) 按不同阶段读取，批次与统计不一致已复现。 |
| 33 | 回调锁 | [选项构造](../../router/registration.go)、[健康命名](../../x/observability/ops/healthhttp/manager.go) 在对应写锁外。日志写入同步串行并有 writer 约束，未模拟第三方不遵守约束。 |
| 34 | 可变所有权 | [健康模型](../../health/core.go) 浅复制嵌套详情， [指标 fan-out](../../metrics/multi.go) 按子项复制标签，其他容器形状见各卡。 |
| 35 | 增长上限 | [缓存](../../store/cache/memory.go) 有载荷限制，[路由池](../../router/pool.go) 有保留上限；Tracker 总历史缺少上限，分页也没有限制全量扫描。 |
| 36 | core 生命周期 | [core 生命周期回归](../../core/lifecycle_test.go) 和 [回滚测试](../../core/lifecycle_group_test.go) 已运行；并发排空、接管归属和准备状态隔离见对应卡。 |
| 37 | router 分派 | [匹配](../../router/matcher.go) 和 [分派](../../router/dispatch.go) 已查，router race 通过。新卡覆盖逆向通配、冻结切换、Content-Length 及缓存冷热。 |
| 38 | contract | [响应](../../contract/response_test.go) 和 [错误链](../../contract/errors_chain_test.go) 已运行，新增矩阵核对正文状态和 RequestID 优先级。 |
| 39 | middleware | [writer 组合](../../middleware/conformance/response_writer_contract_test.go) 通过，gzip 的额外隐式 200 由独立桩复验，其他组合限任务所述。 |
| 40 | security | [JWT 验证](../../security/jwt/verify.go)、[静态凭据比较](../../security/authn/token.go)、[安全头校验](../../security/headers/headers.go) 已读；包现有负例通过。未验证线上代理或真实身份配置。 |
| 41 | store | [KV 状态](../../store/kv/state_regression_test.go)、[缓存](../../store/cache/state_regression_test.go)、[SQL](../../store/db/sql_test.go) 已运行。缩容重开、取消和历史兼容分别验证。 |
| 42 | health | [健康枚举](../../health/core.go) 与 [就绪语义](../../health/readiness.go) 清楚；本机验证 Manager 能判失败，Tracker 仍可能把同批组件记为成功。 |
| 43 | log | [文本边界](../../log/safety_regression_test.go) 和 [JSON 测试](../../log/json_test.go) 通过，短写恢复、双 sink 与文件保留组合待补证。 |
| 44 | metrics | [多集统计](../../metrics/multi.go) 的饱和和非原子快照有公开说明。基线现有并发测试通过，空名与多路饱和后更新另证。 |
| 45 | 定向回归 | 五个临时本机桩观察当前异常行为，未写入仓库。实施卡要求补在所属包的正常入口。 |
| 46 | 负路径 | 当前重点确认 Goexit、分页边界、整数溢出；驱动双错误、请求取消与未发布资源另列。 |
| 47 | 并发证据 | 压缩异常桩以 done 收尾；桩仅使用本机合成输入。任务卡指定通道或受控时钟，不以等待一定时间判定结果。 |
| 48 | 性能证据 | 只确认历史持有和无效格式路径的复杂度。未给绝对耗时承诺；吞吐与分配方案由 1853、1854、1899 验证。 |
| 49 | 格式与 lint | `make fmt-check` 与 `make lint` 通过，0 项发现。源码未修改，未运行原地格式化。 |
| 50 | 门禁 | 本轮通过稳定范围 race、vet、边界、API 及覆盖率。审查文件使用差异门禁检查所选配置。 |
| 51 | 覆盖率 | `make stable-coverage` 为 96.8%，覆盖阈值通过。极端统计、取消和真实多进程部署证据单独记录。 |
| 52 | 文档与交付 | 提交基线、五项本机复现、候审卡和当前命令结果见本报告。实际实现由后续授权触发，对应文档只随已实现行为同步。 |

## 本机复现

| 临时测试 | 当前观察 |
|---|---|
| `TestAbortedAggregateCountedAsComponentSuccess` | 聚合 unhealthy，组件累计 success=2、failure=0。 |
| `TestCompressionGoexitCommitsImplicitHeader` | 只设置 header 后退出，gzip 提交状态序列 [200]，identity 没有状态提交。 |
| `TestHistoryTailOffsetHasMore` | 总数 1、页长 0、最大 int 偏移，HasMore 为 true。 |
| `TestHistoryMalformedNumericFilterSilentlyUsesDefault` | 非整数分页查询为 200，回退到 limit 50 和 offset 0。 |
| `TestAverageLatencyWraps` | 两次相同的合成正耗时，平均值为负。 |

## 验证记录

| 命令 | 结果 |
|---|---|
| `git show --format= --check HEAD` | 通过，基线提交为 a8c2f28a。 |
| `make boundary-check api-check fmt-check lint stable-coverage` | 通过。九个稳定根快照匹配，lint 0 项，覆盖率 96.8%。 |
| `go test -race -timeout 60s ./core ./router ./contract ./middleware/... ./security/... ./store/... ./health ./log ./metrics ./x/observability/ops/healthhttp .` | 通过。 |
| 对同一包集合运行 `go vet` | 通过。 |
| `go test -race -timeout 15s -v /tmp/plumego-a8c-probe_test.go` | 五项当前行为均得到本机验证。临时桩位于工作区外。 |
| `make validate-diff-dry`、`make validate-diff` | 通过，选择 `docs_only`，该配置无额外命令。 |
| 逐卡 `make check-card C=review-a8c2f28a/<卡名>`，并检查路径、链接和行号 | 52 张通过。报告含连续 52 个编号。Git 差异只有 54 个新增 Markdown 文件。 |

本轮取证限于列出的代码路径、合成依赖及当前平台。参考模块全量测试、CLI 全量测试、网站构建、原生跨平台和真实生产部署未重跑。
