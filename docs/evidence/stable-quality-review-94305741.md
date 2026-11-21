# 稳定模块第 9 节质量复审

审查基线为 `94305741`，提交后的代码工作区干净。范围是九个稳定模块及根门面。参考装配、内部 HTTP writer 和健康编排宿主用于核对代表性调用链。

本页保留实施前的证据。已归档卡片、修复后回归和基准见 [实施记录](../../tasks/cards/review-94305741/README.md)。

## P0–P3 发现

本轮记录 **12 项有证据的发现**，其中 4 项通过本机公开入口复现。另有 **41 张补证卡**，用于验证仍缺证据的行为。

| 优先级 | 已复现 | 源码确认 | 补证任务 |
|---|---:|---:|---:|
| P0 | 0 | 0 | 0 |
| P1 | 3 | 0 | 0 |
| P2 | 1 | 6 | 38 |
| P3 | 0 | 2 | 3 |

已复现表示隔离测试观测到行为。源码确认表示可直接沿当前实现追溯。对刻意保留的兼容行为和容量边界，先核对调用方，再确定是否需要行为变更。

### P0

本轮未确认 P0。生产部署、真实凭据和原生平台矩阵均未取证。

### P1

- **1690 未知健康状态在摘要入口映射为成功**（已复现，§9 6,21,38,42）
  - 公开 Manager 返回未知健康状态时，摘要输出 200，而同一模型经 readiness 返回 503。外部监控只消费摘要状态码时会得到成功信号。 [x/observability/ops/healthhttp/helpers.go:66](https://github.com/spcent/plumego/blob/9430574179dc6b793524992833ce27496a5fe548/x/observability/ops/healthhttp/helpers.go#L66)
  - 最小方案。让状态映射显式列出健康和降级，未知或空状态走 503。保留原健康模型和成功响应结构。
  - 回归验收。用同一 Manager 对照未知、空、健康、降级和失败状态，检查摘要和 readiness 的约定。 [任务卡](../../tasks/cards/done/1690-health-unknown-status.md)
- **1691 全量组件端点在编码失败时返回空 200**（已复现，§9 21,22,23,38,42,46）
  - 上一轮修过 writeHealthResponse 的提交前编码，本端点仍忽略 WriteResponse 的失败。包含一个不可编码的人工详情时录得 200、零正文，调用方无法区分有效探测。 [x/observability/ops/healthhttp/handlers.go:129](https://github.com/spcent/plumego/blob/9430574179dc6b793524992833ce27496a5fe548/x/observability/ops/healthhttp/handlers.go#L129)
  - 最小方案。复用健康宿主已有的提交前编码与固定错误处理，并验证写失败之后不会尝试第二次提交。
  - 回归验收。通过 AllComponentsHealthHandler 传入正常详情及不可编码占位值，核对最终状态、结构、request_id 及提交次数。 [任务卡](../../tasks/cards/done/1691-components-encode-failure.md)
- **1692 清理时钟回调重入 Stop 导致互等**（已复现，§9 28,29,33,40,47）
  - 注入的 Config.Now 由清理协程调用。回调同步调用 Stop 后等待同一协程的退出信号，隔离测试定位到两者互等。 [security/abuse/limiter.go:462](https://github.com/spcent/plumego/blob/9430574179dc6b793524992833ce27496a5fe548/security/abuse/limiter.go#L462)
  - 最小方案。明确时钟回调必须可快速返回并与关停分离。若需要支持轻量可重入时钟，后台使用 ticker 时间，把可注入时钟的业务用途和兼容要求先写清楚。
  - 回归验收。用独立进程或合成时间验证最终清理和 Stop 的时钟约定，多调用者仍同步等待实际清理退出。 [任务卡](../../tasks/cards/done/1692-limiter-clock-stop-reentry.md)

### P2

- **1693 正文超限后被舍弃的写入返回成功**（已复现，§9 6,20,22,23,39）
  - 超限产生一次 413 且下游正文被舍弃；下游 Write 的返回却为完整字节数、nil 错误。业务可能误以为已发送，和同一包装的 FlushError 不一致。 [middleware/bodylimit/body_limit.go:107](https://github.com/spcent/plumego/blob/9430574179dc6b793524992833ce27496a5fe548/middleware/bodylimit/body_limit.go#L107)
  - 最小方案。盘点对阻断结果的依赖，再统一 Write 和 FlushError 的拒绝信号；原请求出现的 413 或已提交终态仍只写一次。
  - 回归验收。同时验证提交前、提交后、空写入、写入数与错误分类。 [任务卡](../../tasks/cards/done/1693-bodylimit-write-result.md)
- **1694 正文限制覆写传入请求的 Body**（源码确认，§9 6,18,26,29,34）
  - 正文限制在进入下游前改变同一个 Request。外层保留原请求的适配器在返回后读到包装器；与 requestid 已确定的派生请求所有权不同。 [middleware/bodylimit/body_limit.go:62](https://github.com/spcent/plumego/blob/9430574179dc6b793524992833ce27496a5fe548/middleware/bodylimit/body_limit.go#L62)
  - 最小方案。先核对 Close 及请求对象所有权，再通过浅复制请求把包装器传给下游，并检查外层超时取消。
  - 回归验收。原 Body 标识不变，下游读超限和 Close 正常；两种 timeout 顺序稳定。 [任务卡](../../tasks/cards/done/1694-body-replacement-request-ownership.md)
- **1695 回放超限后缓冲容量仍被保留**（源码确认，§9 16,23,29,35,48）
  - ClearBody 调用 bytes.Buffer.Reset，复用容量保留至 recorder 不可达。原注释说释放内存，迟到 worker 仍可持有对象。 [middleware/timeout/timeout.go:268](https://github.com/spcent/plumego/blob/9430574179dc6b793524992833ce27496a5fe548/middleware/timeout/timeout.go#L268)
  - 最小方案。在确认回放放弃后释放正文底层存储，同时保留已捕获的响应状态和退出路径；同步修正文档。
  - 回归验收。注入大而有界正文超限，检查保留容量、退出及单次错误提交，不观察不相关 GC 波动。 [任务卡](../../tasks/cards/done/1695-timeout-buffer-reset-retention.md)
- **1696 关停诊断回调可覆盖 Shutdown 错误**（源码确认，§9 22,28,33,36）
  - 网络关停返回错误时，先调用注入 logger 再包装错误。logger 抛 panic 会掩盖本应可分类的取消或期限错误。 [core/lifecycle.go:95](https://github.com/spcent/plumego/blob/9430574179dc6b793524992833ce27496a5fe548/core/lifecycle.go#L95)
  - 最小方案。将关停诊断改为局部 best-effort，保留 Shutdown 原 cause；复核是否采用相同的无路由诊断约定。
  - 回归验收。受控活动连接触发期限并配置人工失败的 logger，断言 errors.Is 和后续关停可用。 [任务卡](../../tasks/cards/done/1696-shutdown-diagnostic-panic.md)
- **1697 KV 读取预算超出缺少机器可判结果**（源码确认，§9 14,22,29,41）
  - 预读大小及读取后两条边界都返回仅含格式化文案的错误。调用方只能匹配字符串来区分可提高预算的快照和普通 I/O 错误。 [store/kv/persistence.go:73](https://github.com/spcent/plumego/blob/9430574179dc6b793524992833ce27496a5fe548/store/kv/persistence.go#L73)
  - 最小方案。统一构造读取预算错误，先盘点已有 sentinel 及公开影响；需要新增公开分类时独立走 API 审核。
  - 回归验收。验证两个边界的分类、主 cause 和失败后的目录锁释放。 [任务卡](../../tasks/cards/done/1697-kv-limit-error-classification.md)
- **1698 合并器成功回放未扣减等待者计数**（源码确认，§9 28,29,32,35）
  - 等待者入队递增；取消、超时和失败分支部分递减，但成功回放路径结束后未扣减。当前公开 Stats 只返回活跃键，未发现外部统计错报。 [middleware/singleflight/singleflight.go:265](https://github.com/spcent/plumego/blob/9430574179dc6b793524992833ce27496a5fe548/middleware/singleflight/singleflight.go#L265)
  - 最小方案。删除无人消费的计数，或把等待者所有权统一收束在退出 defer，确保每次只更新一次。
  - 回归验收。覆盖成功、写失败、取消及超时，确认没有受影响的公开结果或钩子。 [任务卡](../../tasks/cards/done/1698-coalescer-waiter-lifetime-accounting.md)
- **1699 内部日志轮换失败时可能丢失原活动链接**（源码确认，§9 29,30,31,43）
  - 旧活动链接先移除，接着创建新链接。创建失败时文件 writer 已切换但链接失效；此处障碍限于内部文件后端。 [log/glog_rotation.go:150](https://github.com/spcent/plumego/blob/9430574179dc6b793524992833ce27496a5fe548/log/glog_rotation.go#L150)
  - 最小方案。在同目录创建临时链接并在成功后替换，失败时保留旧的活动链接，明确 Windows 替换行为。
  - 回归验收。验证新文件继续写入、旧链接在注入失败时可读、没有遗留临时链接。 [任务卡](../../tasks/cards/done/1699-rotation-active-link-publication.md)

### P3

- **1700 观测回调注释将路由覆盖写成整个下游请求**（源码确认，§9 16,27,34,39）
  - 回调收到入站准备后的请求，加上 writer 发布的路由元数据；注释可能让调用者以为下游任意 WithContext 值都回传。 [middleware/internal/telemetry/helpers.go:189](https://github.com/spcent/plumego/blob/9430574179dc6b793524992833ce27496a5fe548/middleware/internal/telemetry/helpers.go#L189)
  - 最小方案。明确复制边界及 span 从响应头等约定来源取得。
  - 回归验收。核对旁路上下文不会传播，公开已知路由仍可读。 [任务卡](../../tasks/cards/done/1700-telemetry-observer-context-comment.md)
- **1701 DB 打开文档缺少连接池延迟建连约定**（源码确认，§9 6,15,16,41）
  - Open 默认调用 sql.Open 并配置 pool，GoDoc 却称打开数据库连接。调用者可能将构造成功作为网络可达判据。 [store/db/sql.go:144](https://github.com/spcent/plumego/blob/9430574179dc6b793524992833ce27496a5fe548/store/db/sql.go#L144)
  - 最小方案。明确返回池句柄，并引用现有 Ping 入口。
  - 回归验收。用桩驱动计数验证配置过程和显式 Ping 的不同阶段。 [任务卡](../../tasks/cards/done/1701-database-open-doc-laziness.md)

## 执行顺序

先处理 1690 和 1691 的健康宿主，再处理 1692 的关停活性。随后处理 P2 的运行边界、P3 说明及补证。兼容决策在各卡指明的公开调用方核对后再确定。53 张卡的执行边界、测试与核验命令见 [实施索引](../../tasks/cards/review-94305741/README.md)。

## 模块取证

使用 `stable_root_boundary_review` 路由和 review 上下文包。已核对九份目标模块清单、根模块依赖和符号处置。根门面无独立清单，另核对了 [健康宿主清单](../../x/observability/module.yaml)。本轮写入范围为本报告与候审批次，对 API、依赖、运行行为、安全策略及现有 active 队列无影响。上一轮归档及 [1615](../../tasks/cards/blocked/1615-kv-os-lock-platform.md) 的原生平台验证仍在原位置。

| 模块 | 公开入口及内部路径 | 代表消费方与证据 |
|---|---|---|
| [core 清单](../../core/module.yaml) | [Prepare / Server / Shutdown](../../core/lifecycle.go)，[准备和发布](../../core/http_handler.go)，[LifecycleGroup](../../core/lifecycle_group.go) | [公开测试](../../core/core_public_test.go)，[标准服务装配](../../reference/standard-service/internal/app/app.go) |
| [router 清单](../../router/module.yaml) | [AddRoute](../../router/registration.go)、[分派](../../router/dispatch.go)、[URL/Routes](../../router/metadata.go)、[StaticFS](../../router/static.go) | [core 注册](../../core/routing.go)，[HEAD 验证](../../router/head_regression_test.go) |
| [contract 清单](../../contract/module.yaml) | [WriteResponse/WriteError](../../contract/response.go)、[APIError](../../contract/errors.go)、[追踪](../../contract/trace.go) | [标准路由装配](../../reference/standard-service/internal/app/routes.go)，[传输测试](../../contract/response_test.go) |
| [middleware 清单](../../middleware/module.yaml) | [Chain](../../middleware/middleware.go)、[请求 ID](../../middleware/requestid/helpers.go)、[gzip](../../middleware/compression/gzip.go)、[timeout](../../middleware/timeout/timeout.go)、[调试错误](../../middleware/debug/debug_errors.go)、[观测](../../middleware/internal/telemetry/helpers.go) | [组合 writer](../../middleware/conformance/response_writer_contract_test.go)，[独立 Chain](../../middleware/middleware_test.go) |
| [security 清单](../../security/module.yaml) | [VerifyToken](../../security/jwt/verify.go)、[策略编译和应用](../../security/headers/headers.go)、[限流](../../security/abuse/limiter.go)、[输入检查](../../security/input/input.go) | [认证适配](../../middleware/auth/auth.go)，[安全头适配](../../middleware/securityheaders/headers.go) |
| [store 清单](../../store/module.yaml) | [缓存](../../store/cache/memory.go)、[KV](../../store/kv/kv.go)、[快照](../../store/kv/persistence.go)、[DB](../../store/db/sql.go)、[文件错误](../../store/file/errors.go)、[幂等](../../store/idempotency/store.go) | [状态回归](../../store/kv/state_regression_test.go)，[连接池与事务](../../store/db/transaction_regression_test.go) |
| [health 清单](../../health/module.yaml) | [HealthState / HealthStatus](../../health/core.go)、[ReadinessStatus](../../health/readiness.go) | [宿主](../../x/observability/ops/healthhttp/manager.go)，[模型测试](../../health/json_contract_test.go) |
| [log 清单](../../log/module.yaml) | [NewLogger](../../log/logger.go)、[JSON 输出](../../log/json.go)、[文本文件生命周期](../../log/glog_lifecycle.go) | [访问日志适配](../../middleware/accesslog/accesslog.go)，[JSON 输出回归](../../log/json_test.go) |
| [metrics 清单](../../metrics/module.yaml) | [Record / ObserveHTTP](../../metrics/collector.go)、[聚合器](../../metrics/multi.go) | [HTTP 观测](../../middleware/httpmetrics/http_metrics.go)，[聚合测试](../../metrics/multi_test.go) |
| 根门面 | [构造和 Param](../../plumego.go) | [hello 示例](../../examples/hello/main.go)，[根包测试](../../plumego_test.go) |

边界门禁通过。运行正确性仍有已记录发现，具体见优先级节。

## 关键状态与所有权

| 路径 | 初始与事件 | 发布、失败和重试边界 |
|---|---|---|
| 健康摘要 | manager 给出状态，宿主映射状态码 | 未知值由稳定模型视为未就绪，摘要实际返回 200。先统一状态映射，再检查完整已知集合。 |
| 组件健康响应 | 获取快照后开始 JSON 编码 | 编码失败发生在提交前。应在该点完成一次安全错误响应；底层已写失败后停止输出。 |
| Limiter 清理 | 周期触发后执行注入时钟与删除过期项 | Stop 发停止信号并等待清理退出。时钟回调内同步 Stop 会互等；收尾行为需要保留原同步约定。 |
| 正文超限 | 下游读取超过限制，包装器进入阻断态 | 未提交时写一次 413，随后正文被舍弃；已提交时须保住该终态。验收同时检查调用者能见到的写入结果。 |
| KV 快照读取 | 获取目录锁后识别读取预算 | 超预算时不发布实例，读取不会改写快照。失败后检查锁释放与重开；现有错误缺少稳定分类。 |
| 文件日志轮换 | 新 writer 就绪，更新活动文件入口 | 链接替换发生在 writer 已切换后，现状先删旧链接；失败时需要核对链接仍可读取以及继续写入的边界。 |

## 第 9 节逐项证据

| 编号 | 规则 | 本轮证据与核验限制 |
|---|---|---|
| 01 | 模块归属 | 已读九根与观测宿主清单。状态模型留在 stable health；HTTP 映射及错误回退归 healthhttp（1690、1691）。 |
| 02 | 依赖方向 | `make boundary-check` 通过。另追踪 core 调 router、观测宿主调 health 和 contract、HTTP 中间件调 security 的方向。 |
| 03 | 薄门面 | [plumego.go](../../plumego.go) 中四个函数、两个别名与批准集合相符；缺省依赖的独立验证见 1742。 |
| 04 | 标准库与内部依赖 | [go.mod](../../go.mod) 未声明第三方模块；内部包调用路径按 [依赖规则](../../specs/dependency-rules.yaml) 与基线门禁核验。 |
| 05 | 显式构造 | [应用装配](../../reference/standard-service/internal/app/app.go) 显式传依赖；typed nil logger 和注入 limiter 的生命周期各有补证任务。 |
| 06 | 稳定兼容 | API 快照一致。超限写入与请求所有权涉及行为兼容（1693、1694）；变更前核对现有调用方。 |
| 07 | 兼容层处置 | 废弃清单门禁通过。[清单](../../specs/deprecation-inventory.yaml) 记录保留信息，已文档化的兼容决策沿用；历史整数读写组合待补证。 |
| 08 | 小范围可逆 | 每张卡指定单一主模块、最多五个文件、最多三条卡内命令。跨模块宿主影响通过相邻卡复核。 |
| 09 | 命名意图 | [等待者计数](../../middleware/singleflight/singleflight.go) 与已公开 Stats 的维度不同，先核实用途及退出时归属（1698）。 |
| 10 | 主流程 | [关停](../../core/lifecycle.go) 主路径可追踪；错误分支先发诊断再还原结果，诊断失败可改变终态（1696）。 |
| 11 | 职责和阶段 | [日志轮换](../../log/glog_rotation.go) 区分 writer 切换与链接发布，后者存在先删除再创建的失败窗口（1699）。 |
| 12 | 辅助函数 | [健康写入函数](../../x/observability/ops/healthhttp/helpers.go) 已为部分入口集中回退。全量组件端点尚有独立写入分支（1691）。 |
| 13 | 小接口 | [指标接口](../../metrics/collector.go) 按 Recorder、HTTPObserver、StatsReader、Resetter 拆分。单目标委托与多目标复制另有候审验证。 |
| 14 | 配置语义 | [KV 选项](../../store/kv/options.go) 与 [DB 池配置](../../store/db/sql.go) 有归一化入口。读取预算分类已定位，DB 重配置和零值待补证。 |
| 15 | 构造错误 | [DB OpenWith](../../store/db/sql.go) 包装配置/建连错误的责任可追踪；缺省 Open 的延迟建连说明有缺口（1701）。 |
| 16 | 约束文档 | 回调时钟重入、观测收尾请求上下文、连接池延迟建连都有可定位的约束或说明缺口（1692、1700、1701）。 |
| 17 | net/http 兼容 | [Router](../../router/dispatch.go) 与 [Chain](../../middleware/middleware.go) 使用标准请求、writer 和 handler；包装器特有能力按 24 和 39 核验。 |
| 18 | 输入来源 | [正文限制](../../middleware/bodylimit/body_limit.go) 处理 Body，[认证](../../security/authn/authn.go) 与 CORS 区分请求头和策略；来源优先级仍有验证卡。 |
| 19 | 可搜索注册 | [标准路由](../../reference/standard-service/internal/app/routes.go) 显示方法、路径、处理函数并处理错误；core 与 router 的拒绝分类组合待核验。 |
| 20 | 中间件控制流 | 本机超限继续访问下游时仅产生一个 413，随后 Write 的局部成功信号与实际分歧（1693）；未枚举所有组合。 |
| 21 | 标准 JSON | [contract](../../contract/response.go) 有标准响应路径；组件宿主丢失编码错误（1691）；正常编码结构仍由已有测试约束。 |
| 22 | 错误分类 | [错误转换](../../contract/errors.go) 支持标准分类；关停诊断可能覆盖 cause，KV 读预算缺少稳定区分，全量健康端点忽略错误。 |
| 23 | 提交边界 | 全量组件编码失败发生在首次提交前；超限正文被丢弃发生在拒绝后。恢复层 Flush 失败与 CORS 最终头部另外补证。 |
| 24 | writer 能力 | [组合用例](../../middleware/conformance/response_writer_contract_test.go) 已执行；补证覆盖 Flush 失败、静态空文件和压缩保留头部。 |
| 25 | 敏感数据 | 核对 [认证](../../security/authn/authn.go)、[日志编码](../../log/json.go) 与 [响应回退](../../contract/response.go) 边界；生产配置、真实凭据、运行采样未取证。 |
| 26 | 取消传播 | [DB](../../store/db/sql.go) 和 [core 关停](../../core/lifecycle.go) 使用调用方 context。接近过期时的 timeout 判决及排水结果保留候审。 |
| 27 | 上下文范围 | [追踪](../../contract/trace.go) 与 [认证 principal](../../security/authn/authn.go) 有独立访问器；观测收尾注释夸大回传边界（1700）。 |
| 28 | 状态转换 | 清理协程时钟重入产生 Stop 互等（1692）；core 绑定失败、嵌套生命周期组和注入 limiter 的后续状态仍需覆盖。 |
| 29 | 资源所有权 | 跟踪 Stop 通道、超时 recorder、连接池和日志文件生命周期。超时缓冲 Reset 仍留分配（1695）；KV 平台卡保留。 |
| 30 | 清理错误 | [KV 失败路径](../../store/kv/persistence.go) 区分替换前清理与替换后问题；日志切换后的旧句柄关闭错误及嵌套回滚待证。 |
| 31 | 提交和重试 | KV 读预算拒绝发生在加载前；日志链接与 writer 的发布存在分离。缓存历史值演进、监听失败重试有验证卡。 |
| 32 | 并发不变量 | 稳定根与 healthhttp 的 race 通过。等待者计数、深路径池、清理覆盖和 aggregate stats 仍需定向断言；race 无法证明活性。 |
| 33 | 回调锁边界 | limiter 清理回调跨越关闭等候依赖；本机栈确认互等。观测收尾、logger panic 路径各自有具体验收。 |
| 34 | 可变数据归属 | 正文限制回写同一 Request（1694）。KV 返回副本、健康/文件 Clone 及单收集器 labels 所有权仍按卡验证。 |
| 35 | 容量 | KV 有读预算；超时 recorder 停止接受回放后仍保留底层容量。路由池、随机池和字段批量编码缺本轮基准。 |
| 36 | core 生命周期 | [生命周期组测试](../../core/lifecycle_group_test.go) 和 core race 已执行；诊断 panic 为源码确认，排水、绑定失败复用、回滚剩余项待证。 |
| 37 | router 匹配 | [分派](../../router/dispatch.go) 与 [HEAD 回归](../../router/head_regression_test.go) 支持匹配和协商；归一化后的 Allow、group 冻结及空文件待补证。 |
| 38 | contract 传输 | [响应测试](../../contract/response_test.go) 已跑。宿主未知状态和编码失败是本轮独立异常；错误树优先级与 details 数值编码待证。 |
| 39 | middleware 组合 | 已运行组合回归。正文拒绝后的下游信号已复现，timeout、CORS、恢复层和 trace 收尾的具体交互列入卡片。 |
| 40 | security 拒绝路径 | [JWT 验证](../../security/jwt/verify.go) 和 [密码比较](../../security/password/password.go) 的现有正负向测试已运行；限流时钟问题是生命周期正确性，本轮未验证生产策略。 |
| 41 | store 一致性 | [KV 状态回归](../../store/kv/state_regression_test.go) 随 race 运行；读取预算分类缺口和历史格式、过期清理、重开副本分别记录。 |
| 42 | health 状态 | [稳定模型](../../health/core.go) 对未知值判定非就绪；摘要与 readiness 状态码不一致且组件失败会空返回，已通过公开宿主入口复现。 |
| 43 | log 完整性 | JSON 和文本现有测试均运行。活动链接发布存在失败窗口；坏字段与部分写入组合、文件关闭诊断未独立取证。 |
| 44 | metrics 一致性 | [并发回归](../../metrics/concurrency_regression_test.go) 随 race 执行。空名称对聚合、单目标身份和 typed nil 构造成本待补证。 |
| 45 | 聚焦回归 | 临时公开入口夹具记录四项可复现行为。各卡给出可观察回归、命令及相关源码；本轮未把临时测试写入仓库。 |
| 46 | 负向路径 | 临时夹具覆盖未知值、不可编码详情、已拒绝写入和回调互等；其余候审的控制条件尚待执行。 |
| 47 | 确定性并发 | race 通过。时钟回调互等通过隔离的超时和栈验证；候审的状态交错使用通道或可控时钟。 |
| 48 | 性能证据 | 本轮未作前后性能结论。已定位缓冲保留原因；路由池、请求 ID、日志字段、指标构造卡要求可重复度量。 |
| 49 | 格式和 lint | `make fmt-check lint` 通过，lint 0 项。本轮无 Go diff。 |
| 50 | Diff 门禁 | 基线跑稳定范围 race、vet、边界、API；候审 Markdown 批次按实际 diff 选择门禁，记录见下。 |
| 51 | 覆盖率 | `make stable-coverage` 实测 96.4%；并发、回滚、失败分支与平台限制由上述条目独立列明。 |
| 52 | 同步和交付 | 基线 commit 固定上一轮实现；候审文档记录优先级、证据、最小方案与限制。没有行为改动需要同步。 |

## 复现夹具

| 卡片 | 本机输入与入口 | 可观察结果 |
|---|---|---|
| 1690 | 自定义 Manager 返回同一个未识别的状态，分别调用 `SummaryHandler` 和 `ReadinessHandlerWithManager`。 | 摘要 200，就绪 503。 |
| 1691 | 自定义 Manager 提供一组组件详情，单项使用通道作为不可编码的占位值，调用 `AllComponentsHealthHandler`。 | 首次提交前编码失败，记录器状态 200、正文 0 字节。 |
| 1692 | 通过通道确认清理协程已进入注入的时钟回调。回调同步调用 Stop，同时观察清理退出和另一调用者的 Stop。 | 隔离进程超时。栈显示清理协程等待自身退出。触发来自进程内部的时钟实现。 |
| 1693 | 对本机 `httptest` 请求设置 1 字节上限，传入 2 字节，处理函数读到超限后调用 Write。 | HTTP 413，尝试写入的字符串不出现在客户端正文，下游收到完整写入计数和 nil 错误。 |

## 验证记录

环境为 Go 1.26.0、darwin/arm64，Go 缓存和 lint 二进制使用临时目录。

| 命令或核验 | 结果 |
|---|---|
| `make boundary-check api-check fmt-check lint stable-coverage` | 通过。稳定 API 快照一致，lint 0 项，稳定模块覆盖率 96.4%。 |
| `go test -race -timeout 90s ./core/... ./router/... ./contract/... ./middleware/... ./security/... ./store/... ./health/... ./log/... ./metrics/... ./x/observability/ops/healthhttp .` | 通过。包含九个稳定根、根门面及健康宿主。 |
| `go vet ./core/... ./router/... ./contract/... ./middleware/... ./security/... ./store/... ./health/... ./log/... ./metrics/... .` | 通过，无诊断。 |
| `go test -v -run '^Test(HealthUnknownSummaryAndReadiness|ComponentsEncodingFailure|BodyLimitBlockedWriteCount)$' /tmp/plumego-audit-943_test.go` | 通过观测断言。摘要/就绪返回 200/503；全量组件返回 200 和空正文；413 之后被丢弃的写入向下游返回 9、nil。 |
| `go test -run '^TestLimiterClockReentrantStop$' -timeout 2s /tmp/plumego-audit-943_test.go` | 复现预期失败。2 秒超时，栈可追踪到清理回调调用 Stop 并等待自身。 |
| `make check-card C=review-94305741/<卡片文件名>` | 53 张通过。首检发现标题式字段未被仓库脚本识别，改为脚本识别的字段后逐张重跑。 |
| `make validate-diff-dry` 和 `make validate-diff` | 通过。选择 `docs_only`，该配置没有额外命令。 |
| Git diff、编号与链接核验 | 通过。52 个检查号、53 个连续卡号，报告和卡片的相对链接、源码行号及 Markdown 行尾均已校验。工作区仅新增 55 个审查 Markdown 文件。 |

两组临时夹具仅在本机执行。本轮未执行完整 CLI、全部参考程序、网站构建和原生跨平台矩阵。Windows 与 Plan 9 原生锁验证仍见 1615。
