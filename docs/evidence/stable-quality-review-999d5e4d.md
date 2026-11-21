# 稳定模块第 9 节质量复审

基线为 `999d5e4d`（完成健康报告与响应提交边界复审修复）。范围为九个稳定模块和根门面，延伸至健康宿主与标准服务的相关调用链。 [质量规则第 9 节](../operations/agent-code-quality-rules.md#L267)

## 基线 P0–P3 发现

本轮通过本机合成测试确认 **5 项问题**。另拆出 50 张待补证卡，共 55 张。

| 优先级 | 已复现 | 源码确认 | 补证任务 |
|---|---:|---:|---:|
| P0 | 0 | 0 | 0 |
| P1 | 1 | 0 | 0 |
| P2 | 3 | 0 | 25 |
| P3 | 1 | 0 | 25 |

复现使用本机合成数据。第 1954、1956 张卡接续上轮只做分析的观察，本轮在新基线上重新验证。补证及各修复的实施记录见本报告末尾。

### P0

未确认。生产配置、真实依赖和部署可达性未取证。

### P1

- **1953 未执行的单组件检查发布失败并挤掉有效检查**（已复现，检查号 6,22,26,31,42,46,47）
  - 已有健康结果时让一次有效检查在回调内等待，再发起预取消的单组件检查。后者未执行回调，仍发布 unhealthy 和零次尝试，有效检查完成后收到 superseded。 [x/observability/ops/healthhttp/manager.go:266](../../x/observability/ops/healthhttp/manager.go#L266)
  - **最小修复** 在抢占版本前处理已取消上下文，并让零次尝试不发布健康状态。保留关闭、未注册和已实际执行检查的返回语义。
  - **验收** 串行和交错情况下，预取消均返回 context.Canceled 且不修改快照或抢占版本。真实失败仍发布，批次路径和手工就绪回归通过。 [任务卡](../../tasks/cards/done/1953-direct-health-cancel-before-admission.md)

### P2

- **1954 单组件跟踪把被更新结果覆盖的成功计成失败**（已复现，检查号 6,31,42,44,46,47）
  - 交错执行的两个回调分别成功与失败，Tracker 最终统计为 0 成功、2 失败。共享快照与返回错误描述最新发布和发布权，无法代表旧调用的回调结果。 [x/observability/ops/healthhttp/tracker.go:241](../../x/observability/ops/healthhttp/tracker.go#L241)
  - **最小修复** 像批次路径一样，内置 manager 用私有单次执行结果更新统计，保持公开返回值和外部 Manager 的快照回退，并让缓存继续遵循版本规则。
  - **验收** 分别覆盖较旧回调成功和失败、未开始时不计数，以及外部 Manager 回退。缓存仍由获准发布的调用决定。 [任务卡](../../tasks/cards/done/1954-direct-tracker-attempt-metrics.md)
- **1955 Debug 在外层恢复 panic 后丢失已提交响应的 trailer**（已复现，检查号 6,17,23,24,39,46）
  - 本机 HTTP 服务中，外层 recovery 正常结束已提交的 202，响应体完整。加 Debug 后，panic 前写入的已声明尾字段消失。正常 Debug 和无 Debug 的恢复对照均保留尾字段。 [middleware/debug/debug_errors.go:132](../../middleware/debug/debug_errors.go#L132)
  - **最小修复** 在调用下游前安排清理，只在已提交透传且未 Hijack 时复制现有尾字段。正常缓冲结果沿用现有提交路径，未提交异常不写响应。
  - **验收** 用真实 HTTP 客户端读取 202、响应体和 trailer。覆盖恢复正常、未提交异常、Hijack 与普通透传，确保没有第二次提交。 [任务卡](../../tasks/cards/done/1955-debug-recovery-committed-trailer.md)
- **1956 重新启用文件轮换时漏算关闭期间写入的内容**（已复现，检查号 32,34,43,46）
  - 内部文件后端禁用轮换期间写到 1048628 字节，按 1 MiB 启用后再写一条，跟踪大小仍为 64 字节。阈值判断忽略禁用时产生的内容。 [log/glog_rotation.go:96](../../log/glog_rotation.go#L96)
  - **最小修复** 在现有串行写入边界继续计入已写文件字节，只暂停阈值判断和归档。避免重复计入已有文件。
  - **验收** 关闭期间文件和计数一致，启用后首条写入按阈值触发轮换。覆盖多个级别、重复切换和无文件实例。 [任务卡](../../tasks/cards/done/1956-file-rotation-reenable-size-accounting.md)

### P3

- **1957 健康 Manager 的 Enabled 字段缺少兼容语义说明**（已复现，检查号 6,14,16,42,52）
  - Config.Enabled 可以设为 true 或 false，两个构造配置都会执行显式检查。构造保存字段，Manager 未用它控制检查，现有字段也没有解释。 [x/observability/ops/healthhttp/manager.go:25](../../x/observability/ops/healthhttp/manager.go#L25)
  - **最小修复** 补齐字段注释与宿主文档，说明显式调用仍执行，以及需要停用时由装配方控制是否挂载或调度。保持零值调用兼容。
  - **验收** 公开说明和 true、false、Config{} 三组观察一致，JSON 字段兼容并通过网站生成校验。 [任务卡](../../tasks/cards/done/1957-health-enabled-compatibility-doc.md)

## 实施计划

先修 1953 的直接检查版本与取消边界，再做 1954 的尝试计量。1955 的 Debug 尾字段与 1956 的文件字节计量可独立处理。1957 说明 Enabled 现有兼容语义。2002、2003 在 1954 后验证关闭与外部 Manager 回退。 [候审索引](../../tasks/cards/review-999d5e4d/README.md)

## 模块取证

使用 `stable_root_boundary_review` 路由与 review 上下文包。九个稳定模块清单、健康宿主清单与稳定依赖约束均已查阅。 [健康宿主清单](../../x/observability/ops/module.yaml)根门面沿用根仓库约束。本轮工作区新增审查 Markdown 文件。计划将来保持公开稳定签名与标准库依赖。

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

边界与稳定 API 快照检查通过。单组件检查和 Tracker 的编排行为由健康宿主负责。

## 状态与执行约束

| 路径 | 观测到的边界 | 实现验收 |
|---|---|---|
| 单组件检查 | 调用注册检查、分配版本、尝试回调、发布缓存 | 未尝试的调用不接管有效检查。注册身份、取消时机与关闭状态分别核验。 |
| Tracker | 已完成回调和提交成功是两个结果 | 组件计数按实际尝试，发布继续用当前 Manager 的身份与版本。 |
| Debug | Flush 移交头与响应体，包装器仍持有尾字段 | 已提交的透传由 wrapper 收尾，连接接管和未提交异常不额外写入。 |
| 文件日志 | 私有文件 writer 累加每次成功写入的实际计数，决定是否轮换 | 禁用只暂停轮换判断，原路径的字节基线保持准确。 |
| 配置兼容 | Enabled 存在于公开配置与 JSON 中，显式检查始终执行 | 说明零值、字段用途与宿主装配责任，保留已有默认调用。 |

## 第 9 节逐项证据

| 编号 | 核查项 | 证据与边界 |
|---|---|---|
| 01 | 所有权 | 依据九个稳定模块清单及 ops 清单，组件执行与统计归健康宿主，健康模型归 health，已提交尾字段归 Debug writer，文件计量归 log。 |
| 02 | 依赖方向 | boundary-check 通过。抽查 core → router、middleware → contract 与健康宿主 → health 的实际调用路径。 [依赖规则](../../specs/dependency-rules.yaml) |
| 03 | 根门面 | 公开便利入口与批准范围一致，公开包测试通过。新增补证核对由门面装配的嵌套路由 Param。 [根门面](../../plumego.go) |
| 04 | 标准库 | 主模块依赖声明和边界门禁已核对，当前计划复用标准库与现有内部传输能力。 [根 go.mod](../../go.mod) |
| 05 | 显式装配 | 标准服务显式传入依赖，恢复构造接收 logger，健康 Tracker 接收 Manager。连接其他实际宿主的运行时配置未取证。 [服务装配](../../reference/standard-service/internal/app/app.go) [恢复构造](../../middleware/recovery/recover.go) |
| 06 | 稳定契约 | 九根 API 快照匹配。直接健康检查、跟踪与 Debug 的发现来自运行时契约，Enabled 保留原有零值语义。 |
| 07 | 兼容层 | 严格 deprecation inventory 通过。SQL 便捷 QueryRow 与 KV 非 Context 调用仍有现行入口，本轮不涉及迁移。 [兼容清单](../../specs/deprecation-inventory.yaml) |
| 08 | 可逆范围 | 每卡登记唯一主模块，至多五个候选文件和三条命令。更新健康内部接口、外部回退的兼容验证分卡。 |
| 09 | 命名与单位 | 检查文件 currentSize 的字节单位与 MaxSize 的 MiB 单位，发现关轮换时未累加已写字节。 [轮换实现](../../log/glog_rotation.go) |
| 10 | 主执行顺序 | 单组件先抢占版本，再在重试入口检查上下文。已复现零次尝试先失去旧发布权再发布失败。 [执行与发布](../../x/observability/ops/healthhttp/manager.go) |
| 11 | 阶段职责 | 生命周期组单独执行准备、回滚和返回错误；单次跟踪应区分检查尝试与缓存发布。嵌套 Ready 继续补证。 [生命周期组](../../core/lifecycle_group.go) |
| 12 | 辅助函数 | 批次跟踪已从内置 manager 读取私有尝试结果，单次路径可复用原内部能力。反向路由名称和参数键值验证用独立输入检查。 [跟踪实现](../../x/observability/ops/healthhttp/tracker.go) [路由元数据](../../router/metadata.go) |
| 13 | 接口粒度 | ComponentChecker、Observer 和 Tracer 仅暴露各入口的调用需求。nil Observer 和 nil Tracer 的 writer 透传单列核验。 [健康模型](../../health/core.go) [HTTP 指标](../../middleware/httpmetrics/http_metrics.go) [追踪](../../middleware/tracing/tracing.go) |
| 14 | 配置语义 | Enabled 真实执行行为通过 true 与 false 对照。没有停用传入检查和组件真实运行的门控；兼容含义缺少说明。 [现行配置](../../x/observability/ops/healthhttp/manager.go) |
| 15 | 构造失败 | Manager 检查超时与重试值，Run 保留非关闭错误。先 Prepare 后遇到占用地址、再 Shutdown 的分类列入补证。 [启动实现](../../core/lifecycle.go) |
| 16 | 约束说明 | 健康宿主已说明调用时检查及周期调度。Enabled 缺少相应字段注释。状态模型空值和 wire 形状由卡片核验。 [健康宿主文档](../modules/x/observability/ops/README.md) |
| 17 | HTTP 接口 | 静态文件、Debug 与限流均使用标准 handler 和 writer。通过本机 HTTP 客户端实测 Debug 与 recovery 的交叠。 [Debug](../../middleware/debug/debug_errors.go) [静态文件](../../router/static.go) |
| 18 | 输入来源 | 历史读取 query，CORS 从请求头提取预检字段，bodylimit 读取请求体。CORS 合法重复头输出及 body 边界关闭单列。 [历史入口](../../x/observability/ops/healthhttp/history.go) [CORS](../../middleware/cors/cors.go) |
| 19 | 注册错误 | 标准服务装配可搜索方法、路径与 handler，core 注册继续返回内部错误。组空相对路径处理与元数据同步尚待核对。 [标准服务路由](../../reference/standard-service/internal/app/routes.go) [core 路由](../../core/routing.go) |
| 20 | middleware 顺序 | recovery 对已提交状态结束恢复，正常情况下直接使用底层已声明尾字段。已复现加入 Debug 后尾字段消失。CORS 重复普通头待核查。 [恢复](../../middleware/recovery/recover.go) |
| 21 | 规范 JSON | 健康历史 JSON 使用 contract.WriteResponse，格式错误使用 contract.WriteError，CSV 走独立表示。类型化错误和 205 矩阵待验。 [历史入口](../../x/observability/ops/healthhttp/history.go) [响应入口](../../contract/response.go) |
| 22 | 错误分类 | context.Canceled 能被识别，另一有效检查仍返回 superseded。SQL 无记录与 nil *Row 的错误分类独立补证。 [检查实现](../../x/observability/ops/healthhttp/manager.go) [SQL 帮助函数](../../store/db/sql.go) |
| 23 | 提交归属 | 本机 HTTP 对照的 202 和普通实体均完整。丢失点局限在 Debug 已透传后的尾字段，后续校验未提交和 Hijack 保持既有归属。 [Debug](../../middleware/debug/debug_errors.go) |
| 24 | writer 能力 | 稳定包竞态测试包含组合 writer 用例。本轮另用 ResponseController.Flush 触发实际提交，验证有无 Debug 和有无恢复的可观察差异。 [组合测试](../../middleware/conformance/response_writer_contract_test.go) |
| 25 | 敏感数据 | 检查恢复分类和健康跟踪固定诊断，合成执行未读取生产日志、凭据或私有出站依赖。 [恢复日志](../../middleware/recovery/recover.go) [统计记录](../../x/observability/ops/healthhttp/tracker.go) |
| 26 | 取消传播 | 预取消从 Manager 返回 context.Canceled，但导致发布零次尝试。不执行的请求、实际回调和 Close 的交叠各有验收。 [管理器](../../x/observability/ops/healthhttp/manager.go) |
| 27 | Context 边界 | 请求标识、路由状态与 trace 使用各自私有键，未在本次选定路径发现服务定位用法。无采样 trace 和已准备 Server 的 context 另行补证。 [路由上下文](../../contract/context_core.go) [追踪上下文](../../contract/trace.go) |
| 28 | 阶段状态 | 核查 Core 启动和停止、缓存 Clear 的进入条件、文件轮换开关。缓存预取消后计量是否维持通过专门卡复验。 [生命周期组](../../core/lifecycle_group.go) [缓存](../../store/cache/memory.go) |
| 29 | 资源所有权 | 静态文件 defer Close、限流 body Close 委托、快照替换及 SQL Rows 清理来自不同拥有方。底层 body 关闭的原错误通过定向夹具继续查证。 [静态文件](../../router/static.go) [限流读取](../../middleware/bodylimit/body_limit.go) [SQL](../../store/db/sql.go) |
| 30 | 清理错误 | DB 和 KV 检查到主错误与关闭或回滚的归并入口。当前目标平台的既有用例通过，其他平台的目录同步行为无新运行证据。 [SQL](../../store/db/sql.go) [KV 持久化](../../store/kv/persistence.go) |
| 31 | 发布与重试 | 健康回调实际执行、管理器版本提交和统计读取属不同阶段。交错测试确认一次预取消写坏快照和一次旧实际结果被错误计数。 |
| 32 | 同步不变量 | 稳定 race 通过。健康两项复现均以通道控制先后，日志配置的内部大小与真实文件单独对照。并发 race 的通过不能证明业务不变量成立。 |
| 33 | 回调锁 | 路由选项处理与最终登记分阶段，健康在锁外执行回调。若移动版本分配点，仍需验证未持锁运行组件回调。 [路由注册](../../router/registration.go) [健康检查](../../x/observability/ops/healthhttp/manager.go) |
| 34 | 可变所有权 | HealthStatus 明确仅浅复制详情的值。文件元数据的重复引用、Allow 响应头和 child 指标快照另证。 [健康 Clone](../../health/core.go) [文件元数据](../../store/file/types.go) |
| 35 | 资源上界 | 小页历史扫描仍遍历候选，结果复制按页宽约束。缓存容量替换、无排队槽位和底层读取预算有分卡。 [历史查询](../../x/observability/ops/healthhttp/tracker.go) [快照限长](../../store/kv/persistence.go) |
| 36 | core | 核对 Run、Prepare、Server、Shutdown 和 LifecycleGroup，相关包验证通过。监听失败分类、嵌套 Ready 和冻结前后 nil middleware 待补证。 |
| 37 | router | 查看精确与通配匹配、隐式 HEAD、Allow、反向 URL、静态两入口。相同模式的清单顺序、显式 HEAD 和重叠匹配分卡。 [分派](../../router/dispatch.go) [反向 URL](../../router/metadata.go) |
| 38 | contract | 查看 WriteResponse、WriteError、错误归一化与上下文快照。测试基线通过，类型优先级及 205 不调用编码器待补证。 [响应](../../contract/response.go) [错误](../../contract/errors.go) |
| 39 | middleware | 组合入口及子包 race 通过。额外真实 HTTP 测试确认 Debug 使外层恢复后的 trailer 丢失；其余配置或容量核对作为待补证。 |
| 40 | security | 抽查认证校验、JWT、策略编译及限流负例。以合成数据核对 TTL、合法策略和长度，不涉及生产策略或信任配置。 [JWT 检查](../../security/jwt/verify.go) [策略](../../security/headers/headers.go) |
| 41 | store | cache/KV/SQL/file/idempotency 的现有用例通过。此次新增候审内容聚焦删除无变化快照、资源错误归类和元数据所有权，未扩充平台覆盖。 |
| 42 | health | 健康模型现有用例通过。宿主实际确认零次尝试发布、Tracker 与执行状态分歧和 Enabled 行为说明问题。响应与关闭交叠仍按卡验收。 |
| 43 | log | 文本分流与 JSON 归一化现有测试通过。补充对内部文件轮换配置的开关复现，重复目标路径只列待核。 [文本写入](../../log/glog_writer.go) [JSON](../../log/json.go) |
| 44 | metrics | 查看 child 快照、串行 Clear、活动序列和饱和合并。基础计数边界只列合成分析。已确认的错计发生在扩展 Tracker。 [基础指标](../../metrics/collector.go) [聚合](../../metrics/multi.go) |
| 45 | 定向回归 | 本机探针从普通入口比对健康、统计、HTTP 与 Enabled，内部日志路径用同包 overlay。正式回归由各修复卡补齐。 |
| 46 | 负例 | 验证预取消、较新检查失败、普通 panic 恢复和内部轮换关闭。真实外部服务失败、原生跨平台失败路径仍未覆盖。 |
| 47 | 并发证据 | 两种并发健康场景使用通道与原子计数。未来编排用通道或条件等待，记录顺序，减少对系统调度时机的依赖。 |
| 48 | 性能测量 | 无本轮修复前后对照。重跑现有小页分页基准以复核分配数量级，CSV 稀疏匹配的扫描时间与完整匹配语义保留候审。 |
| 49 | 格式与 lint | make fmt-check 与 make lint 通过，lint 0 项。仅记录审查 Markdown，源码无需原地格式化。 |
| 50 | 差异门禁 | 稳定范围 race、vet、边界、API 与 coverage 已运行。审查产物落盘后按 docs_only 配置核验，未启动实现阶段修改。 |
| 51 | 覆盖率 | 稳定模块总语句覆盖率 96.8%。实际缺陷均有另外的本机观察。覆盖率无法替代取消、提交和关闭的专项验收。 |
| 52 | 交付记录 | 本报告保存提交基线、优先级、五项发现、55 张候审卡及验证范围。涉及已公开行为的文档同步留在对应实现卡。 |

## 本机复现

| 临时测试 | 当前观察 |
|---|---|
| `TestCanceledDirectReplacesHealthySnapshot` | 预取消返回 context.Canceled，已缓存状态 healthy → unhealthy，尝试数为 0，并使另一项有效检查返回 superseded。 |
| `TestTrackerDirectStatsFollowPublishedSnapshotWhenSuperseded` | 两个实际回调各成功、失败一次，Tracker 记录为成功 0、失败 2。 |
| `TestDebugTrailerOnRecoveredCommittedResponse` | 三组均返回 202 和完整响应体，只有 Debug 包装后再由外层恢复的响应缺少尾字段。 |
| `TestPrivateRotationEnablesFromTrackedBytes` | 关闭期间文件内容达 1048628 字节，开启后写入一条，大小计数为 64 字节。 |
| `TestHealthEnabledFlagDoesNotControlManager` | true 与 false 两组都执行了一个已注册组件，无行为差异。 |

这些临时探针断言并记录基线行为，运行成功表示问题已观察到。三组临时源码均在工作区外。日志探针使用 Go overlay 加载同包测试，仓库源文件未改动。

## 验证记录

| 命令 | 结果 |
|---|---|
| `git show --format= --check HEAD` | 通过，基线为 999d5e4d。 |
| `make boundary-check api-check fmt-check lint stable-coverage` | 通过。九个稳定根 API 快照匹配，lint 0 项，稳定语句覆盖率 96.8%。 |
| `go test -race -timeout 60s ./core ./router ./contract ./middleware/... ./security/... ./store/... ./health ./log ./metrics ./x/observability/ops/healthhttp .` | 通过。 |
| 对同一包集运行 `go vet` | 通过。 |
| `go test -race -v -timeout 25s /tmp/review999_probe_test.go` | 三组探针通过，确认预取消、尾字段、Enabled。 |
| `go test -race -timeout 25s -v /tmp/2b94-analysis-boundaries_test.go -run '^TestTrackerDirectStatsFollowPublishedSnapshotWhenSuperseded$'` | 通过，确认尝试被错计。 |
| `go test -overlay=/tmp/2b94-log-overlay.json -race -timeout 40s -v ./log -run '^TestPrivateRotationEnablesFromTrackedBytes$'` | 通过，确认内部计量缺口。 |
| `go test ./x/observability/ops/healthhttp -run '^$' -bench '^BenchmarkHistoryFirstSmallPage$' -benchmem -benchtime=100ms -count=3` | 通过。100 与 10000 条历史，小页均为 96 B/op、1 alloc/op。三次中位数分别为 291.6 和 27624 ns/op。仅建立当前扫描与分配基线。 |
| `make validate-diff-dry`、`make validate-diff` | 通过，选中 docs_only。该配置没有附加命令。 |
| 逐卡执行 `make check-card C=review-999d5e4d/<卡名>`，并核验 Markdown 内链 | 55 张卡校验通过。检查号 01–52 完整，352 条链接与行号均可解析。 |
| `git diff HEAD --name-only`、`git ls-files --others --exclude-standard` | 基线提交后仅新增 57 个审查 Markdown 文件，源码与测试未修改。 |

本轮取证限于列出的代码路径、合成依赖及当前平台。参考模块全量测试、CLI 全量测试、网站构建、原生跨平台和真实生产部署未重跑。

## 实施结果

1953–2007 已按计划完成并归档。1953–1956 的四组行为回归用 999d5e4d 的生产实现与同一组新测试进行对照，四组均复现，修复后连续十次竞态测试通过。1957 更新了配置语义说明。其余 47 张核对可观测现有契约并补回归或基准。 [实施索引](../../tasks/cards/review-999d5e4d/README.md)

| 路径 | 准入、提交与重试规则 |
|---|---|
| 单组件检查 | 在上下文仍可用且注册身份一致时取得执行版本。已准入的首次回调在锁外执行。零次尝试不改快照，调用者可用新上下文重试。关闭或更新版本会拒绝旧结果。 |
| 单组件统计 | 已完成的尝试参与计数，发布错误原样返回。关闭后继续保留已有快照及已经执行的计量，新的尝试被拒绝。 |
| 已透传 Debug 响应 | 响应头、正文已提交时，尾字段在退出时同步。未提交异常继续由恢复层选择响应，连接接管由应用负责。 |
| 文本日志轮换 | 当前文件接收的字节始终计入大小，禁用时跳过阈值判断。重新启用后根据实际大小决定下一次轮换。 |

分析卡 1995 在固定时钟下复现同名目标的计量重置，1998 在预置极限计数下复现整数回绕，两项未改行为。1995 的真实时钟频率和 1998 的真实用量可达性仍未取证。2000 证明逐子收集器重置可与记录交错，符合文档约定。本批未改变这三个分析卡对应的实现。

历史 CSV 基准固定 10000 条记录，一页一条。在本机三次测量中，密集与稀疏筛选的中位数分别为 58738 和 41347 ns/op。两组均为 6378 B/op 与 29 allocs/op。这组结果用来记录当前行为与成本。

### 实施验证

| 命令或检查 | 结果 |
|---|---|
| `make fmt`、`make test` | 通过。根模块常规测试通过。 |
| 新回归用覆盖四个基线生产文件的 Go overlay 运行 | 四组行为测试因预期断言失败。分别观察到预取消抢占有效检查、Tracker 错计、尾字段丢失、轮换禁用期间计量缺失。 |
| 对四组修复及 Enabled 合约运行定向竞态测试，`-count=10` | 通过。 |
| `make lint` | 通过，0 项。 |
| `make website-sync`、`make website-gates` | 通过，生成数据与现有版本一致。构建报告了既有的 Starlight CSS `:global` 压缩警告。 |
| `make check-card C=done/<卡名>`、链接和行号核验、`git diff --check` | 55 张已完成卡、52 个检查项以及 412 条归档、代码和证据链接均通过校验。 |
| `make validate-diff-dry`、`make validate-diff` | 选中跨模块门禁，`make gates` 通过。包含边界、稳定 API、文档示例、vet、根模块竞态测试、参考应用和 CLI。 |
| `make stable-coverage` | 通过，九个稳定模块语句覆盖率 96.8%。 |
