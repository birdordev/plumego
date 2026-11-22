# 稳定模块第 9 节质量审查

基线为 `6e8d53f9d0537080d6d0e701cdacf2982e3a07a3`，审查日期为 2026-11-22（北京时间）。覆盖九个稳定模块、根门面，以及第 42 项要求沿调用链核对的健康宿主。依据第 8 节判定 P0–P3，第 9 节逐项记录代码、测试证据和验证限制。 [质量规则](../operations/agent-code-quality-rules.md#L267)

## P0–P3 发现

本轮确认 8 项，另有 17 张验证任务卡。P0 没有确认项。生产配置、真实数据库故障和跨平台运行不属于本机证据范围。

| 优先级 | 已复现 | 源码确认 | 待补证 | 合计 |
|---|---:|---:|---:|---:|
| P0 | 0 | 0 | 0 | 0 |
| P1 | 2 | 1 | 0 | 3 |
| P2 | 1 | 2 | 10 | 13 |
| P3 | 0 | 2 | 7 | 9 |
| 合计 | 3 | 5 | 17 | 25 |

### P1

- **保留 gzip 外层响应头和既有编码**（已复现，检查号 6,17,23,24,34,39,45）
  - 触发与结果。启用 gzip 时，包装器从空私有 Header 开始。外层已声明 gzip 并交给下游写入预编码字节时，下游读不到外层头，输出经过第二次压缩，但对外仍只有一层编码声明。 [middleware/compression/gzip.go:453](../../middleware/compression/gzip.go#L453) [middleware/compression/gzip.go:414](../../middleware/compression/gzip.go#L414) [middleware/compression/gzip_test.go:708](../../middleware/compression/gzip_test.go#L708)
  - 最小修复。从外层头初始化本地快照，并在同一终态提交路径同步下游对继承字段的新增、替换和删除。保持 Vary、临时 1xx 和 trailer 的所有权。
  - 回归验收。先用外层预设编码和合成正文锁定字节一致性，再覆盖删除继承字段、不同层追加 Vary 及内外层的 trailer。 [任务卡 2119](../../tasks/cards/done/2119-gzip-inherits-response-headers.md)
- **阻止旧批次失败覆盖替换组件的就绪状态**（已复现，检查号 26,28,31,32,42,45,47）
  - 触发与结果。旧批次阻塞期间，同名组件被替换并通过单项检查。旧结果不能覆盖组件状态，但仍进入全局失败列表，导致 Ready 从 true 变为 false，同时 Components 中该项为 true。 [x/observability/ops/healthhttp/manager.go:318](../../x/observability/ops/healthhttp/manager.go#L318) [x/observability/ops/healthhttp/manager.go:371](../../x/observability/ops/healthhttp/manager.go#L371) [x/observability/ops/healthhttp/manager.go:395](../../x/observability/ops/healthhttp/manager.go#L395) [x/observability/ops/healthhttp/aggregate_regression_test.go:169](../../x/observability/ops/healthhttp/aggregate_regression_test.go#L169)
  - 最小修复。在批次发布临界区校验注册身份与检查版本，丢弃过期的发布资格，独立保留真实检查尝试的历史和计量。覆盖注销、同代真实失败以及已关闭的终态。
  - 回归验收。用通道卡住旧检查，替换并验证新组件后释放旧失败，断言旧批次不改写较新的就绪状态。另核对关闭和手动标记仍优先。 [任务卡 2120](../../tasks/cards/done/2120-stale-health-batch-readiness.md)
- **移出 KV 锁内的调用方上下文方法**（源码确认，检查号 26,30,31,32,33,41,46）
  - 触发与结果。公开读写入口在持有读锁或写锁时调用调用方提供的 Context.Err。自定义上下文同步读取同一 store 时会与写锁互等，普通标准库上下文未显示此问题。 [store/kv/kv.go:113](../../store/kv/kv.go#L113) [store/kv/kv.go:169](../../store/kv/kv.go#L169) [store/kv/kv.go:355](../../store/kv/kv.go#L355) [store/kv/kv.go:532](../../store/kv/kv.go#L532)
  - 最小修复。锁外取得取消信号，持锁时检查通道。需要错误原因时先恢复未提交状态并释放锁，再读取错误。保留重命名前后对取消和同步失败的既有规则。
  - 回归验收。由测试上下文记录 Err 是否能读取同一存储，以通道控制锁和取消。验证取消后内存、磁盘和重开一致，以及已提交路径不回滚。 [任务卡 2121](../../tasks/cards/done/2121-kv-context-check-lock-boundary.md)

### P2

- **保留请求合并首个已提交响应的尾部字段**（已复现，检查号 17,23,24,29,39,45,46）
  - 触发与结果。首个请求写出 201 和正文，填入已声明的 trailer 后 panic。已提交状态和正文保留，尾部值只在正常返回时复制，恢复后尾部为空。 [middleware/singleflight/singleflight.go:308](../../middleware/singleflight/singleflight.go#L308) [middleware/singleflight/singleflight.go:330](../../middleware/singleflight/singleflight.go#L330) [middleware/singleflight/response_regression_test.go:386](../../middleware/singleflight/response_regression_test.go#L386)
  - 最小修复。把首个响应的 trailer 发布移到异常路径也运行的收尾。只发布已提交且没有接管连接的尾部值。保持 panic 原因与等候者的失败判定。
  - 回归验收。覆盖正常返回、已提交后的 panic 和 Goexit、提交前异常、接管连接后退出。断言状态写一次，等候者无部分响应重放。 [任务卡 2122](../../tasks/cards/done/2122-singleflight-committed-trailer-abnormal-exit.md)
- **在生命周期准入中避免锁内调用上下文实现**（源码确认，检查号 26,28,32,33,36,47）
  - 触发与结果。Start 在 g.mu 临界区调用外部 Context.Err。同步检查组状态的自定义上下文可重入同一把锁，回调本身的非阻塞重入保证覆盖不到准入阶段。 [core/lifecycle_group.go:134](../../core/lifecycle_group.go#L134) [core/lifecycle_group.go:153](../../core/lifecycle_group.go#L153) [core/lifecycle_group_test.go:739](../../core/lifecycle_group_test.go#L739)
  - 最小修复。锁外读取取消信号，临界区只检查信号和 phase。取消时保持 idle，解锁后返回错误，维持已经启动的错误优先级。
  - 回归验收。以自定义上下文和通道确认检查发生于锁外。断言预取消后仍能 Add 并启动，重叠 Start 仍按原契约返回。 [任务卡 2123](../../tasks/cards/done/2123-lifecycle-start-context-lock-boundary.md)
- **消除 KV 锁等待取消测试的固定延迟**（源码确认，检查号 26,45,46,47）
  - 触发与结果。测试靠 20 毫秒延迟让 goroutine 进入等待，调度慢时也可能在入口发现预取消而通过，无法证明实际覆盖了持锁等待。 [store/kv/kv_test.go:905](../../store/kv/kv_test.go#L905) [store/kv/kv_test.go:919](../../store/kv/kv_test.go#L919)
  - 最小修复。用明确的准入信号或 testing/synctest 等待 goroutine 阻塞，再取消并释放锁。
  - 回归验收。重复运行，断言取消返回的分类、无新键和重开无写入。 [任务卡 2129](../../tasks/cards/done/2129-kv-cancel-test-barrier.md)

### P3

- **合并重复的无名指标断言**（源码确认，检查号 9,12,44,45,47）
  - 触发与结果。两项回归都断言无名计数与 Clear。另一个用例名把固定样本规模称为容量边界，实际只写入 256 个名字。 [metrics/collector_test.go:243](../../metrics/collector_test.go#L243) [metrics/collector_test.go:263](../../metrics/collector_test.go#L263) [metrics/collector_test.go:295](../../metrics/collector_test.go#L295)
  - 最小修复。整合共同断言，保留通用名称、HTTP 名称、快照隔离和起始时间的独立断言，并按实际样本重命名 256 名称用例。
  - 回归验收。对照合并前的断言集合，核对功能矩阵和 race 测试。 [任务卡 2125](../../tasks/cards/done/2125-metrics-unnamed-regression-overlap.md)
- **明确 App 与组注册辅助函数的作用域**（源码确认，检查号 9,12,16,19,52）
  - 触发与结果。App 私有 helper 的注释声称覆盖全部注册。组入口实际走独立 helper，跟踪冻结和错误分类时容易漏读组分支。 [core/routing.go:12](../../core/routing.go#L12) [core/routing.go:150](../../core/routing.go#L150) [core/routing_test.go:625](../../core/routing_test.go#L625)
  - 最小修复。收窄注释术语，说明 App 入口与 RouteGroup 各自入口以及共同委托的 Router.AddRoute。
  - 回归验收。复用已有根与组注册错误分类、冻结优先级测试。 [任务卡 2126](../../tasks/cards/done/2126-core-registration-helper-doc.md)

## 执行顺序

先做 **2119**（压缩响应表示），随后按 **2120 → 2121 → 2122** 处理健康状态发布、KV 取消锁边界及已提交的尾部字段。2135 等待 2119。其余验证卡可单独执行，索引记录本轮排序和前置关系。 [25 张任务卡](../../tasks/cards/review-6e8d53f9/README.md)

| 实现风险 | 验收重点 |
|---|---|
| 响应头快照同时影响编码、Vary 和 trailer | 保留外层元数据及下游对头字段的操作，分别验证普通响应、已编码响应与异常退出后的已提交响应。 |
| 健康批次失去发布资格时，可能漏计已完成的实际检查 | 分别断言已发布的就绪状态、批次终态和尝试次数。覆盖注册替换、手动标记和关闭。 |
| 取消判断移动会改变 KV 的提交竞争 | 校验重命名前可完整撤销，重命名后内存与重开结果一致，取消错误及目录同步错误保持可分类。 |

## 模块取证

审查采用 `stable_root_boundary_review` 和 `review` 上下文包。已读以下九个稳定模块清单及健康宿主清单。报告和任务卡属于交付材料，本轮模块行为变更的目标清单不适用。代码位置按基线记录。

| 模块 | 公共入口与内部路径 | 代表性调用方、测试或限制 |
|---|---|---|
| [core](../../core/module.yaml) | [App / 构造](../../core/app.go#L37)、[Prepare / Server / Shutdown](../../core/lifecycle.go#L46)、[LifecycleGroup](../../core/lifecycle_group.go#L94) | [标准服务](../../reference/standard-service/internal/app/app.go)、[生命周期回归](../../core/lifecycle_group_test.go) |
| [router](../../router/module.yaml) | [AddRoute / Group](../../router/registration.go)、[匹配、缓存及 HEAD](../../router/dispatch.go)、[URL](../../router/metadata.go#L42)、[Static / StaticFS](../../router/static.go#L73) | [core 注册](../../core/routing.go)、[路由契约](../../router/router_contract_test.go)、[HEAD 回归](../../router/head_regression_test.go) |
| [contract](../../contract/module.yaml) | [WriteResponse / WriteError](../../contract/response.go)、[APIError / 分类与详情复制](../../contract/errors.go)、[请求上下文](../../contract/context_core.go)、[TraceContext](../../contract/trace.go) | [标准服务路由](../../reference/standard-service/internal/app/routes.go)、[响应与编码回归](../../contract/response_test.go) |
| [middleware](../../middleware/module.yaml) | [Chain.Build](../../middleware/middleware.go#L129)、[gzip](../../middleware/compression/gzip.go)、[singleflight](../../middleware/singleflight/singleflight.go)、[timeout](../../middleware/timeout/timeout.go)、[bodylimit](../../middleware/bodylimit/body_limit.go) | [标准服务装配次序](../../reference/standard-service/internal/app/app.go)、[响应 writer 组合](../../middleware/conformance/response_writer_contract_test.go)、[请求栈失败回归](../../middleware/stack_failure_regression_test.go) |
| [security](../../security/module.yaml) | [固定凭据校验](../../security/authn/token.go#L23)、[JWT 验证](../../security/jwt/verify.go#L10)、[签名验证](../../security/jwt/signing.go)、[密码哈希](../../security/password/password.go#L232)、[限流](../../security/abuse/limiter.go)、[安全头](../../security/headers/headers.go) | [认证适配](../../middleware/auth/auth.go)、[JWT 负例](../../security/jwt/jwt_negative_matrix_test.go)、[头字段负例](../../security/headers/headers_negative_matrix_test.go)。未测部署策略。 |
| [store](../../store/module.yaml) | [KV 提交](../../store/kv/persistence.go#L210)、[MemoryCache](../../store/cache/memory.go#L37)、[SQL 事务](../../store/db/sql.go#L258)、[文件契约](../../store/file/file.go#L15)、[幂等状态](../../store/idempotency/store.go#L127) | [KV 重开和失败](../../store/kv/state_regression_test.go)、[SQL 驱动替身](../../store/db/transaction_regression_test.go)。未执行跨平台文件锁和真实数据库故障。 |
| [health](../../health/module.yaml) | [HealthState / HealthStatus](../../health/core.go)、[ReadinessStatus](../../health/readiness.go) | [模型序列化测试](../../health/json_contract_test.go)、[健康宿主](../../x/observability/ops/module.yaml)、[宿主发布与关闭](../../x/observability/ops/healthhttp/manager.go) |
| [log](../../log/module.yaml) | [NewLogger](../../log/logger.go#L89)、[JSON](../../log/json.go)、[文本写入](../../log/glog_writer.go)、[轮换与清理](../../log/glog_rotation.go) | [访问日志适配](../../middleware/accesslog/accesslog.go)、[输出与轮换测试](../../log/glog_test.go)、[容量说明](../modules/log/README.md#L113) |
| [metrics](../../metrics/module.yaml) | [Record / Clear / GetStats](../../metrics/collector.go#L137)、[聚合](../../metrics/multi.go) | [HTTP 观察适配](../../middleware/httpmetrics/http_metrics.go)、[并发回归](../../metrics/concurrency_regression_test.go)、[扇出测试](../../metrics/multi_test.go) |
| [根门面](../../plumego.go) | [配置别名与构造](../../plumego.go#L29)、[Param](../../plumego.go#L64) | [公开入口回归](../../plumego_test.go)、[依赖方向规则](../../specs/dependency-rules.yaml)。门面由根规则和快照验证。 |

边界门禁、清单、公开入口、弃用清单和稳定 API 快照检查通过。继续由 `x/observability/ops` 负责健康编排和 HTTP 发布。P1/P2 中确认的问题均有归属模块内的最小修复路径。

## 第 9 节逐项核对

“已核对”指所列入口和样本有代码或现有回归证据。“有发现”表示本表对应检查包含已确认发现。“待补证”标出具体缺口及相关验证卡。

| 编号 | 检查 | 状态与证据 |
|---:|---|---|
| 1 | 模块归属 | 已核对。九个清单和公共入口见模块取证，健康编排跟到宿主。 [稳定模块归属](../../specs/repo.yaml) |
| 2 | 依赖方向 | 已核对。依赖门禁通过，健康宿主引用模型。 [依赖规则](../../specs/dependency-rules.yaml) |
| 3 | 根门面 | 已核对。仅六项批准导出，构造转交 core，参数读取转交 router。准备失败后的组合场景见 2141。 [门面](../../plumego.go#L29) |
| 4 | 标准库及内部依赖 | 已核对。根 go.mod 只有模块和 Go 版本声明，依赖与声明调用方门禁通过。 [go.mod](../../go.mod) [依赖规则](../../specs/dependency-rules.yaml) |
| 5 | 显式装配 | 已核对。App 和标准服务使用构造参数，健康组件显式注册。只核对模块主入口。 [App 构造](../../core/app.go#L37) [标准服务](../../reference/standard-service/internal/app/app.go) [健康组件注册](../../x/observability/ops/healthhttp/manager.go#L149) |
| 6 | 稳定调用契约 | 有发现。API 快照匹配。gzip 重复编码影响响应体，健康就绪发生跨版本混合。 [gzip](../../middleware/compression/gzip.go#L453) [健康批次](../../x/observability/ops/healthhttp/manager.go#L395) |
| 7 | 兼容层处置 | 已核对。严格弃用门禁通过，KV 的 Options 和 DefaultOptions 有明确替代入口。 [弃用清单](../../specs/deprecation-inventory.yaml) [KV 别名](../../store/kv/options.go#L23) |
| 8 | 独立可逆 | 已核对。卡片各有一个主模块、一组明确文件以及可单独验证的行为或证据。 [任务卡索引](../../tasks/cards/review-6e8d53f9/README.md) |
| 9 | 命名 | 有发现。固定 256 个名的测试称为容量边界，另有同义用例。 [指标测试](../../metrics/collector_test.go#L243) |
| 10 | 主流程可读 | 已核对。响应先编码再提交，KV 有前置检查、变更、持久化与失败恢复。 [响应](../../contract/response.go#L22) [KV 写入](../../store/kv/kv.go#L113) |
| 11 | 职责与阶段 | 已核对。生命周期有独立启动与逆序清理，快照写入与目录同步分别区分提交。 [生命周期](../../core/lifecycle_group.go#L178) [持久化](../../store/kv/persistence.go#L210) |
| 12 | 辅助函数 | 有发现。App 私有注册函数注释所称范围超过实际范围，指标测试存在重复。 [App 注册](../../core/routing.go#L12) [组注册](../../core/routing.go#L150) |
| 13 | 窄接口 | 已核对。HTTP 观察者只依赖指标单一能力，健康检查器有 Name 和 Check。 [HTTPObserver](../../metrics/collector.go#L86) [ComponentChecker](../../health/core.go#L19) |
| 14 | 配置语义 | 已核对。KV 零值走默认，负数拒绝。超时非正值禁用，根门面显式零配置有测试。 [KV 配置](../../store/kv/options.go#L45) [超时配置](../../middleware/timeout/timeout.go#L96) [门面测试](../../plumego_test.go#L194) |
| 15 | 可诊断的构造 | 已核对。KV、SQL、限流提供返回错误的入口。现有日志构造的无效格式 panic 已写入 GoDoc。 [KV](../../store/kv/kv.go#L71) [SQL](../../store/db/sql.go#L152) [限流](../../security/abuse/limiter.go#L245) [日志](../../log/logger.go#L89) |
| 16 | 约束文档 | 有发现。私有注册 helper 的作用域需澄清。聚合 stats 独立读取及日志预算边界已说明。 [core](../../core/routing.go#L12) [聚合统计](../../metrics/multi.go#L75) [日志预算](../modules/log/README.md#L113) |
| 17 | net/http 兼容 | 有发现。组装签名保持标准库类型。gzip 包装器私有 Header 与外层视图不同，导致表示变化。 [Chain](../../middleware/middleware.go#L70) [gzip](../../middleware/compression/gzip.go#L224) |
| 18 | 输入来源 | 已核对。路径由 URL.Path 读取，认证凭据由头字段读取，正文经 Body 包装。 [路径](../../router/dispatch.go#L188) [认证](../../security/authn/token.go#L35) [正文](../../middleware/bodylimit/body_limit.go#L42) |
| 19 | 可搜索的路由注册 | 已核对。应用装配可追踪方法与路径，AddRoute 传播注册失败。 [标准服务路由](../../reference/standard-service/internal/app/routes.go) [注册](../../core/routing.go#L34) |
| 20 | 中间件控制流 | 已核对。认证拒绝、并发队列提前返回及分派有现有负例。整体在途容量见 2128。 [认证](../../middleware/auth/auth.go#L52) [并发准入](../../middleware/concurrencylimit/concurrency_limit.go#L58) [栈回归](../../middleware/stack_failure_regression_test.go) |
| 21 | 规范 JSON 输出 | 已核对。标准服务调用 contract，方法拒绝使用结构化错误，普通未匹配走标准库。 [标准服务路由](../../reference/standard-service/internal/app/routes.go) [方法分派](../../router/dispatch.go#L188) |
| 22 | 错误分类 | 已核对。错误链保留值优先级及 SQL 主因与清理错误。取消同时触发驱动清理的分类见 2139。 [APIError](../../contract/errors.go#L357) [SQL](../../store/db/sql.go#L273) |
| 23 | 提交前后的错误 | 有发现。请求合并异常时丢失已提交响应的尾部字段。 [首个请求收尾](../../middleware/singleflight/singleflight.go#L308) |
| 24 | writer 能力 | 有发现。核对了 Flush、接管、HEAD 及已有 1xx 和无正文测试。接管与回放、压缩和 HEAD 组合仍有卡片。 [HEAD writer](../../router/dispatch.go#L116) [gzip writer](../../middleware/compression/gzip.go#L473) [组合回归](../../middleware/conformance/response_writer_contract_test.go) |
| 25 | 敏感输出 | 已核对。抽查 JWT 固定错误、日志回退和 middleware 输出路径。业务传入的任意字段和部署日志不在本机证据范围。 [JWT 适配](../../security/jwt/auth_jwt.go#L132) [日志回退](../../log/json.go#L260) [遥测策略](../../middleware/internal/telemetry/policy.go) |
| 26 | 取消传播 | 有发现。KV 与 LifecycleGroup 在锁内调用外部上下文方法，标准库取消等待用例仍需区分等待前后二阶段。 [KV SetContext](../../store/kv/kv.go#L113) [LifecycleGroup.Start](../../core/lifecycle_group.go#L153) |
| 27 | 上下文职责 | 已核对。路由、trace、请求 ID、Principal 通过包内空结构体键与访问器提供。 [路由键](../../contract/context_core.go#L80) [TraceContext](../../contract/trace.go#L50) [Principal](../../security/authn/authn.go#L48) |
| 28 | 状态转换 | 有发现。既有生命周期具备回滚和重试，但旧健康批次仍能发布失败，详见 2120。 [生命周期阶段](../../core/lifecycle_group.go#L108) [健康发布](../../x/observability/ops/healthhttp/manager.go#L394) |
| 29 | 资源所有权 | 已核对。静态文件、SQL rows、KV 临时文件和 cache 清理均有关闭路径。两个 Close 等待同一写入待补证。 [静态资源](../../router/static.go#L180) [SQL rows](../../store/db/sql.go#L384) [cache](../../store/cache/memory.go#L443) |
| 30 | 主错误及清理错误 | 已核对。生命周期与 KV 会汇合清理错误，SQL context 与驱动交界待核对。 [生命周期](../../core/lifecycle_group.go#L235) [快照清理](../../store/kv/persistence.go#L245) |
| 31 | 提交与重试 | 有发现。KV 以重命名为提交点，锁内上下文检查需要保持该语义。旧健康批次也需在最终发布识别资格。 [KV](../../store/kv/persistence.go#L210) [健康批次](../../x/observability/ops/healthhttp/manager.go#L378) |
| 32 | 整体同步 | 有发现。健康单项报告和整体就绪的代际校验不同。metrics 基础计数使用同一锁，聚合读取的非原子限制已写明。 [健康单项](../../x/observability/ops/healthhttp/manager.go#L299) [基础指标](../../metrics/collector.go#L145) [聚合](../../metrics/multi.go#L75) |
| 33 | 回调锁边界 | 有发现。生命周期组件回调通常在锁外执行，准入中的 Err 例外。KV 有相同的锁内调用。日志 writer 的重入限制已有文档。 [生命周期](../../core/lifecycle_group.go#L153) [KV](../../store/kv/kv.go#L113) [日志 writer](../../log/logger.go#L33) |
| 34 | 可变数据所有权 | 有发现。gzip 没有继承外层快照。KV、health 和 metrics 的代表性读取各有明确复制策略。 [gzip](../../middleware/compression/gzip.go#L453) [KV](../../store/kv/kv.go#L163) [health](../../health/core.go#L43) [metrics](../../metrics/multi.go#L47) |
| 35 | 容量上界 | 待补证。路由缓存以条目计数，KV 和 cache 有预算。singleflight 整体在途大小与 struct 日志预算已有代码观察，缺少代表性测量。 [路由缓存](../../router/cache.go#L7) [KV 读取预算](../../store/kv/persistence.go#L62) [日志文档](../modules/log/README.md#L113) |
| 36 | core 生命周期 | 有发现。回滚、清理重试、Ready 间隔有回归，Start 准入锁边界缺回归。 [核心生命周期测试](../../core/lifecycle_group_test.go) [Start](../../core/lifecycle_group.go#L153) |
| 37 | router 优先级 | 已核对。现有测试覆盖死支、完整匹配、HEAD 和 Allow，组合缓存及真实静态 FS 场景见 2130、2131。 [路由契约](../../router/router_contract_test.go) [匹配优先级](../../router/matcher.go#L30) |
| 38 | contract 传输 | 已核对。空对象、编码失败、短写及无正文状态有回归，103 后最终响应组合见 2132。 [响应测试](../../contract/response_test.go) |
| 39 | middleware 次序 | 有发现。确认 gzip 外层继承和 singleflight 异常后收尾问题。标准服务装配与栈测试提供次序证据。 [标准服务装配](../../reference/standard-service/internal/app/app.go) [组合测试](../../middleware/stack_integration_test.go) |
| 40 | security 拒绝路径 | 已核对。已有过期、无效算法、签名与认证失败的负例，固定凭据比较使用定长摘要及 constant-time 比较。时钟取样中的取消口径见 2136。 [JWT 验证](../../security/jwt/verify.go#L10) [JWT 负例](../../security/jwt/jwt_negative_matrix_test.go) [固定凭据比较](../../security/authn/token.go#L72) |
| 41 | store 持久化一致性 | 有发现。历史快照、原子替换、目录同步和重开已有回归。锁内上下文和等待时序列为 2121、2129。 [快照](../../store/kv/persistence.go#L210) [KV 回归](../../store/kv/state_regression_test.go) |
| 42 | health 状态准确 | 有发现。模型已检查未知状态和就绪语义。宿主仍可能由过期检查降级就绪，新的 HTTP 联合负例见 2140。 [模型](../../health/core.go#L61) [宿主发布](../../x/observability/ops/healthhttp/manager.go#L400) |
| 43 | log 输出完整性 | 已核对。整数精度、控制字符、循环和编码器异常有回归。公开文档允许 struct 交付标准 JSON 编码器，资源规模见 2127。 [JSON 回归](../../log/json_test.go) [文本转义](../../log/glog_writer.go#L268) [JSON 预算](../modules/log/README.md#L113) |
| 44 | metrics 统计一致性 | 已核对。基础收集器并发、清空、计数及平面扇出标签隔离测试通过，嵌套扇出见 2143。 [并发统计](../../metrics/concurrency_regression_test.go) |
| 45 | 定向回归 | 有发现。三项本机行为偏差需要公开入口测试，拟改动均给出断言，未将临时探针纳入仓库。 [任务卡索引](../../tasks/cards/review-6e8d53f9/README.md) |
| 46 | 按风险覆盖负例 | 有发现。补齐旧组件失败、已提交异常退出以及锁等待取消。既有测试覆盖多数普通失败路径。 [生命周期回归](../../core/lifecycle_group_test.go) [KV 失败回归](../../store/kv/state_regression_test.go) |
| 47 | 确定性并发测试 | 有发现。KV 测试靠固定等待推断已进入锁等待，同类剩余测试未逐项迁移。 [KV 等待测试](../../store/kv/kv_test.go#L905) |
| 48 | 性能证据 | 待补证。本轮提出容量与基线卡，保留现有路由及指标基准作参照。 [路由基准](../../router/router_bench_test.go) [指标基准](../../metrics/bench_test.go) |
| 49 | 格式与 lint | 格式检查通过。当前执行环境缺少 golangci-lint，零告警门禁待复验。 [Makefile](../../Makefile#L28) |
| 50 | 按 diff 选择门禁 | 已核对文档审查范围，基线单独跑过竞态、vet、边界与稳定 API。实现阶段按各卡实际改动选门禁。 [门禁规则](../../specs/gate-profiles.yaml) |
| 51 | 覆盖率与关键行为 | 稳定模块汇总 96.8%。三个本机偏差仍需永久回归。真实 DB、持久化崩溃、生产配置及跨平台回归留待后续。 [覆盖率入口](../../Makefile#L78) |
| 52 | 文档同步与交付记录 | 公开 API 和可执行文档检查通过。本轮交付限定为报告、卡片和队列索引，实现后逐卡处理公开说明。 [交付合同](../operations/agent-code-quality-rules.md#L177) [任务卡索引](../../tasks/cards/review-6e8d53f9/README.md) |

## 验证记录

本机为 Darwin/arm64，Go 1.26.0。默认 Go 编译缓存路径读取被沙箱拒绝。重跑使用 `GOCACHE=/tmp/plumego-review-go-cache`。根模块测试、静态检查和临时探针的工作区代码均为该基线。

| 命令 | 状态、观察与后续 |
|---|---|
| `make fmt-check` | 通过。 |
| `make test-race` | 通过。覆盖根模块及根模块内扩展包。 |
| `go vet ./...` | 通过。 |
| `make boundary-check api-check doc-check stable-coverage` | 通过。稳定 API 快照匹配，7 个 Go 文档片段通过，稳定覆盖率 96.8%。 |
| `go test -race -count=1 -v -timeout 15s /tmp/plumego-audit-6e8-behavior_test.go` | 验证探针给出 3 个已确认失败和 2 个对照观察。gzip 读不到外层字段，输出字节改变。健康复验得到 `Ready=false`、`Components[sample]=true`，初始 `Ready=true`。请求合并保留 201 但 trailer 为空。gzip 隐式 Flush 后正常发布的 trailer 对照通过。日志对照呈现 1024 和 1400 项，符合现有说明。 |
| `make validate-diff-dry`、`make validate-diff` | 均通过。识别为 `docs_only`，该配置未启动 Go 命令。 |
| `make check-card C=active/<卡片>` | 25 张均通过。根门面没有独立 `module.yaml`，2141 返回一条预期的清单提示。复核 28 个交付文件的相对链接、行号和 52 项编号。 |
| `make lint` | 当前 PATH 无 `golangci-lint`，无法执行。需要在有仓库 v2 lint 工具的环境重跑。 |

临时探针用本机 HTTP recorder、合成载荷、同步通道和 [健康宿主公开入口](../../x/observability/ops/healthhttp/manager.go#L76)。完整 `make gates` 的独立 reference 和 CLI 负载、网站门禁以及外部系统与跨平台测试本轮未执行。当前证据适合作为回归起点，后续代码验收按卡片执行。


## 实施与复验

25 张卡均已结案。8 张确认项已实施，3 张补证卡确认行为差异后实施，另外 14 张完成了定向取证。公开 Go 签名和依赖保持原样。 [任务卡与结案记录](../../tasks/cards/review-6e8d53f9/README.md)

补证后实施了三项。请求合并将 101 保留给首个请求，等待方收到结构化失败；超时缓冲按已提交的长度返回 `http.ErrContentLength`；压缩中间件在 HEAD 只有头字段时省略可能属于不同表示的长度与强校验头。 [请求合并](../../middleware/singleflight/singleflight.go) [超时](../../middleware/timeout/timeout.go) [压缩](../../middleware/compression/gzip.go)

临时探针通过公开入口或 Go overlay 执行。基准使用 Darwin/arm64、Go 1.26.0、`-benchmem -benchtime=200ms -count=3`。样本和观察记录在单张卡中。真实数据库故障及其他平台上的运行频率仍超出本次证据范围。

| 实施验证 | 结果 |
|---|---|
| `make fmt`、`git diff --check` | 通过。 |
| `make validate-diff-dry` | 选中跨模块配置 `cross_module_public_api_release`。 |
| `make validate-diff` | 通过。执行完整 `make gates`，覆盖 Go 源码、竞态、边界、稳定 API、独立参考服务和 CLI。 |
| `make stable-coverage` | 通过。稳定模块覆盖率 96.8%。 |
| `make lint` | 通过。使用 CI 固定的 golangci-lint v2.14.0，0 项结果。 |
| `make website-sync`、`make website-gates` | 通过。生成文件与提交基线一致，网站构建 184 页。 |
| `make check-card C=done/<卡片>` | 25 张均通过，根门面仍有无独立清单的预期提示。 |
