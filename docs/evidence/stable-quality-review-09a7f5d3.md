# 稳定模块第 9 节质量复审

审查基线为 `09a7f5d3`，提交后的代码工作区干净。范围是九个稳定模块及根门面。参考装配、内部 HTTP writer 和健康编排宿主用于核对代表性调用链。

本页记录修复前的行为和源码位置。当前回归、基准及各卡实施结果见 [实施记录](../../tasks/cards/review-09a7f5d3/README.md)。

## P0–P3 发现

本轮取得 **13 项有证据的发现**，其中 5 项做了本机公开入口验证。其余 **45 张为补证任务**。P0 无已确认发现，生产配置与所有外部消费者位于本机验证范围之外。

| 优先级 | 已复现 | 源码确认 | 补证任务 |
|---|---:|---:|---:|
| P0 | 0 | 0 | 0 |
| P1 | 1 | 0 | 0 |
| P2 | 4 | 6 | 42 |
| P3 | 0 | 2 | 3 |

已复现表示隔离测试观测到行为。源码确认表示可直接沿当前实现追溯。对刻意保留的兼容行为和容量边界，先核对调用方，再确定是否需要行为变更。

### P1

- **1632 JSON 日志写入器重入时锁住调用方**（已复现，§9 29,32,33,43,47）
  - Output.Write 通过同一 logger 记录写入诊断时，外层持有与衍生 logger 共享的写锁，内层再次等待该锁。两秒隔离测试超时，堆栈定位到相同日志入口。 [log/json.go:165](../../log/json.go#L165)
  - 最小方案。先明确同步 sink 的禁止重入契约，并盘点是否已有回调需要独立诊断通道。需要库内支持时另定缓冲、并发及错误语义，避免直接去锁导致输出交错。
  - 回归验收。用隔离进程验证同步写入约定；普通并发及共享 sink 下每条 JSON 仍完整，故障诊断通过独立路径。 [任务卡](../../tasks/cards/done/1632-sink-reentry.md)

### P2

- **1633 HEAD 被拒绝的 Flush 提前结束长度推断**（已复现，§9 6,23,24,37）
  - 最终状态已逻辑选定后，实际 writer 返回 ErrNotSupported，路由层仍先 commit(false)。同一三字节响应不尝试 flush 时头中有长度 3，尝试被拒绝后没有长度。 [router/dispatch.go:130](../../router/dispatch.go#L130)
  - 最小方案。区分确认支持的 flush 与拒绝，保留无实际 flush 时的长度和嗅探能力。先明确能力探测与发送已选最终状态的次序。
  - 回归验收。覆盖显式状态、ErrNotSupported、传输故障和成功 flush 四条路径；最终状态和正文规则维持原样。 [任务卡](../../tasks/cards/done/1633-head-selected-flush-refusal.md)
- **1634 独立 Chain 的 nil builder 在 Build 时 panic**（已复现，§9 6,15,20,45）
  - 独立 NewChain 和 Use 接受空中间件，Build 直接调用空函数产生运行时 panic。App.Use 自己拒绝 nil 只保护 core 装配。 [middleware/middleware.go:131](../../middleware/middleware.go#L131)
  - 最小方案。确认独立 Chain 对 nil builder 的策略，在 Build 入口统一以既有 nil handler 失败方式报告，补齐 GoDoc 和调用方判空。
  - 回归验收。对 NewChain 与 Use 两种输入检查 Build 的结果，确认外层构建函数不会运行，健康链仍按顺序执行。 [任务卡](../../tasks/cards/done/1634-standalone-chain-nil.md)
- **1635 附加请求 ID 时原请求头与衍生请求共用写入**（已复现，§9 6,16,27,34）
  - WithContext 只浅拷贝请求。Header 非 nil 时，给返回请求写入 ID 同时改变传入请求。现有测试断言下游可见，且测试注释把启用描述为回写。原请求是否应被修改尚无明确公开约定。 [middleware/requestid/helpers.go:37](../../middleware/requestid/helpers.go#L37)
  - 最小方案。先明确原请求头是有意回写还是只配置下游；选择隔离时只在修改头的分支克隆 Header，并迁移明确依赖回写的调用。
  - 回归验收。分别断言启用、禁用、原始 Header 为 nil 及带既有字段时两侧的值与隔离。 [任务卡](../../tasks/cards/done/1635-requestid-header-ownership.md)
- **1636 Additional 的规范化同名项导致输出取决于 map 顺序**（源码确认，§9 14,32,34,40,46）
  - 校验分别接受大小写不同且均合法的字段名，应用时把两项规范化为同一名并依次覆盖。冲突配置的最终响应缺少可预测优先级。 [security/headers/headers.go:286](../../security/headers/headers.go#L286)
  - 最小方案。在 Validate 统一对 Additional 规范化名字并拒绝冲突，保持单个 Additional 对标准字段的既有优先级。
  - 回归验收。以普通无关自定义头覆盖重复配置、单个大写变体和无写入的错误返回。 [任务卡](../../tasks/cards/done/1636-policy-header-name-conflict.md)
- **1637 tracer 返回的 SpanID 在输出前没有统一验证**（源码确认，§9 18,25,27,39）
  - BeginTrace 仅在上下文字段回退时检查 Valid，直接从 span 读取的非空值可进响应头，其格式可能与上下文有效性不同。输出依赖注入的 tracer。 [middleware/internal/telemetry/helpers.go:116](../../middleware/internal/telemetry/helpers.go#L116)
  - 最小方案。先将 span 输出与派生载体统一经已有 TraceContext 验证再发出，保留底层 span 的结束责任。
  - 回归验收。使用非敏感诊断占位值验证无效元数据被忽略、合法记录仍存在、span 结束一次。 [任务卡](../../tasks/cards/done/1637-trace-outbound-id-validation.md)
- **1638 显式正文预览直接复制应用错误正文**（源码确认，§9 21,25,35,39）
  - IncludeBody 是开发模式的显式开关。当前只按字节截断，应用错误正文中的任意敏感字段依然可能进入重组后的响应。部署启用情况未核实。 [middleware/debug/debug_errors.go:378](../../middleware/debug/debug_errors.go#L378)
  - 最小方案。盘点显式调用后，优先改为只报告大小和是否截断；如需正文保留能力，先确定经过调用者审核的允许字段契约。
  - 回归验收。用无敏感数据的占位正文核对默认和启用路径的元数据，保留状态、缓存和 request_id 语义。 [任务卡](../../tasks/cards/done/1638-debug-preview-policy.md)
- **1639 缓存的 payload 配额无法约束空值条目数量**（源码确认，§9 14,29,35,41,48）
  - Set 的成本只计 value 长度，空值条目仍进入 sync.Map 并保留键及元数据。现有 GoDoc 如实说明 payload 配额，调用方若将其当作堆内存上限会低估成本。 [store/cache/memory.go:233](../../store/cache/memory.go#L233)
  - 最小方案。先强化容量说明并测量空值与短键的成本；保留已有配额契约，需条目限额时独立评估授权。
  - 回归验收。固定小 payload 限额比较多条空值、普通覆盖、Clear 后的成本和计数。 [任务卡](../../tasks/cards/done/1639-cache-empty-payload-budget.md)
- **1640 数据库构造错误直接格式化注入 opener 的错误文本**（源码确认，§9 22,25,30,41）
  - OpenWith 将自定义 opener 接收的底层错误直接作为可读文本拼接，数据库适配器错误可能携带连接配置片段。当前库只能保证自身参数校验直接报字段名。 [store/db/sql.go:166](../../store/db/sql.go#L166)
  - 最小方案。保持已承诺的 errors.Is/As 分类；为默认对外字符串提供固定诊断，明确底层 cause 的消费者安全责任。改动前检查现有日志和分类调用。
  - 回归验收。用人工标记错误验证默认文本及命名字段输出，确保分类和部分成功的清理错误仍可获取。 [任务卡](../../tasks/cards/done/1640-db-open-safe-diagnostics.md)
- **1641 手机号字数校验分配完整 rune 切片**（已复现，§9 9,12,35,48）
  - ValidatePhone 只返回数字位数是否合法，却按输入字节数预分配 rune 切片。基础有效号码为 0 次分配，含多个分隔符的有效样例为 1 次。 [security/input/input.go:417](../../security/input/input.go#L417)
  - 最小方案。改用整数计数，超过位数上限立即拒绝，保留标点、开头加号和 Unicode 校验规则。
  - 回归验收。公开入口覆盖 7/15/16 位和格式边界，用基准比对同一组普通输入的 allocs/op。 [任务卡](../../tasks/cards/done/1641-phone-digit-count-cost.md)
- **1642 并发 Prepare 单次发布但可重复读 TLS 文件**（源码确认，§9 14,28,31,36,48）
  - 开始时仅快照已发布服务器和中间件构建状态，确认证书后才尝试构建。并发成功只会安装一个 server，但未完成的调用可各自做文件 I/O。 [core/http_handler.go:93](../../core/http_handler.go#L93)
  - 最小方案。测量有证书场景后考虑为 server-only 阶段单独预留资格，保留外部构建器可重入与读取失败后的重试。
  - 回归验收。受控并发验证已发布指针唯一、证书读取次数及失败不冻结的顺序。 [任务卡](../../tasks/cards/done/1642-prepare-tls-work-ownership.md)

### P3

- **1643 KV 主文档仍推荐已标记废弃的配置入口**（源码确认，§9 6,7,16,52）
  - 模块说明推荐 DefaultOptions，但实现的废弃注释明确替代为 DefaultConfig。读主文档的新增接入方会继续采用兼容符号。 [docs/modules/store/README.md:158](../modules/store/README.md#L158)
  - 最小方案。改用 DefaultConfig 做首选并在兼容段引用旧入口；保留已有清单决定。
  - 回归验收。核对 Options 兼容测试和 API 快照，运行生成文档检查。 [任务卡](../../tasks/cards/done/1643-kv-canonical-config-primer.md)
- **1644 日志构造器只使用首个 variadic 配置**（源码确认，§9 6,14,16）
  - 签名允许传多个配置，GoDoc 只解释无配置和格式选项。实现静默忽略后续配置。 [log/logger.go:95](../../log/logger.go#L95)
  - 最小方案。明确使用第一个配置的兼容语义，并记录推荐单配置用法。
  - 回归验收。锁定两个配置冲突时所选格式和目的地。 [任务卡](../../tasks/cards/done/1644-logger-config-arity-doc.md)

## 执行顺序

建议先处理 1632 的重入边界，再处理 1633 的 HEAD 长度推断、1634 的空 builder、1635 的请求头所有权。其余发现按影响及兼容性取证排队。58 张卡的执行边界、测试与核验命令见 [候审索引](../../tasks/cards/review-09a7f5d3/README.md)。

## 模块取证

使用 `stable_root_boundary_review` 路由和 review 上下文包。已核对九份目标模块清单、根模块依赖和符号处置。根门面无独立清单。本轮写入范围为本报告与候审批次，对 API、依赖、运行行为、安全策略及现有 active 队列无影响。上一轮归档及 [1615](../../tasks/cards/blocked/1615-kv-os-lock-platform.md) 的原生平台验证仍在原位置。

| 模块 | 公开入口及内部路径 | 代表消费方与证据 |
|---|---|---|
| core | [Prepare / Server / Shutdown](../../core/lifecycle.go)，[准备和发布](../../core/http_handler.go)，[LifecycleGroup](../../core/lifecycle_group.go) | [公开测试](../../core/core_public_test.go)，[标准服务装配](../../reference/standard-service/internal/app/app.go) |
| router | [AddRoute](../../router/registration.go)、[分派](../../router/dispatch.go)、[URL/Routes](../../router/metadata.go)、[StaticFS](../../router/static.go) | [core 注册](../../core/routing.go)，[HEAD 验证](../../router/head_regression_test.go) |
| contract | [WriteResponse/WriteError](../../contract/response.go)、[APIError](../../contract/errors.go)、[追踪](../../contract/trace.go) | [标准路由装配](../../reference/standard-service/internal/app/routes.go)，[传输测试](../../contract/response_test.go) |
| middleware | [Chain](../../middleware/middleware.go)、[请求 ID](../../middleware/requestid/helpers.go)、[gzip](../../middleware/compression/gzip.go)、[timeout](../../middleware/timeout/timeout.go)、[调试错误](../../middleware/debug/debug_errors.go)、[观测](../../middleware/internal/telemetry/helpers.go) | [组合 writer](../../middleware/conformance/response_writer_contract_test.go)，[独立 Chain](../../middleware/middleware_test.go) |
| security | [VerifyToken](../../security/jwt/verify.go)、[策略编译和应用](../../security/headers/headers.go)、[限流](../../security/abuse/limiter.go)、[输入检查](../../security/input/input.go) | [认证适配](../../middleware/auth/auth.go)，[安全头适配](../../middleware/securityheaders/headers.go) |
| store | [缓存](../../store/cache/memory.go)、[KV](../../store/kv/kv.go)、[快照](../../store/kv/persistence.go)、[DB](../../store/db/sql.go)、[文件错误](../../store/file/errors.go)、[幂等](../../store/idempotency/store.go) | [状态回归](../../store/kv/state_regression_test.go)，[连接池与事务](../../store/db/transaction_regression_test.go) |
| health | [HealthState / HealthStatus](../../health/core.go)、[ReadinessStatus](../../health/readiness.go) | [宿主](../../x/observability/ops/healthhttp/manager.go)，[模型测试](../../health/json_contract_test.go) |
| log | [NewLogger](../../log/logger.go)、[JSON 输出](../../log/json.go)、[文本文件生命周期](../../log/glog_lifecycle.go) | [访问日志适配](../../middleware/accesslog/accesslog.go)，[JSON 输出回归](../../log/json_test.go) |
| metrics | [Record / ObserveHTTP](../../metrics/collector.go)、[聚合器](../../metrics/multi.go) | [HTTP 观测](../../middleware/httpmetrics/http_metrics.go)，[聚合测试](../../metrics/multi_test.go) |
| 根门面 | [构造和 Param](../../plumego.go) | [hello 示例](../../examples/hello/main.go)，[根包测试](../../plumego_test.go) |

边界门禁通过。运行正确性仍有已记录发现，具体见优先级节。

## 关键状态与所有权

| 路径 | 初始与事件 | 发布、失败和重试边界 |
|---|---|---|
| core 准备 | TLS 校验完成后进入 handler 构建，构建冻结注册 | 成功 handler 可先于 server 发布。nil 或 panic 构建释放资格、保留冻结。对重叠 TLS 验证与 Goexit 分别取证。 |
| HEAD writer | 最终状态逻辑确定，头等待实际提交 | 正常完成可推断长度。当前显式状态后的 Flush 即使被拒绝也先结束长度推断。 |
| JSON 日志 | 序列化后持共享写锁调用外部 sink | 正常写完成或报错后释放锁。重入会等待该锁；衍生 logger 共享相同输出序列化资源。 |
| KV | 更新暂存后写临时文件并替换 | 替换前错误恢复内存并收集清理错误。替换后目录同步失败保留新快照；重试先检查已发布状态。 |
| 请求 ID | 为请求创建新的 Context 承载 ID | 返回的新请求与原请求共享非空 Header map。启用回写时两者均看到变更。 |
| 健康模型 | 健康状态与就绪数据由宿主产生，模型 Clone 复制顶层容器 | 调度、缓存、取消与关闭的结果由宿主决定。未知状态在稳定模型中不就绪。 |

## 第 9 节逐项证据

| 编号 | 规则 | 本轮证据与边界 |
|---|---|---|
| 01 | 模块归属 | 读取九根清单，核对稳定设施、HTTP 适配器与宿主。healthhttp 仍拥有调度和 HTTP 响应，稳定 health 只提供模型。 |
| 02 | 依赖方向 | `make boundary-check` 通过。middleware 经清单允许的 internal helper 共享 writer 和观测逻辑，根模块无新增反向依赖。 |
| 03 | 薄门面 | [plumego.go](../../plumego.go) 四个函数及两个别名。上一轮示例注册判错已提交，独立 Param 用例留给 1689。 |
| 04 | 标准库与内部依赖 | 核对 [go.mod](../../go.mod) 和 [依赖规则](../../specs/dependency-rules.yaml)。根模块没有外部依赖；内部依赖按门禁验证。 |
| 05 | 显式构造 | [标准装配](../../reference/standard-service/internal/app/app.go) 显式传 logger 和中间件；[NewAbuseGuard](../../middleware/abuseguard/abuse_guard.go#L122) 明确注入和内部拥有的 limiter。 |
| 06 | 稳定兼容 | 九根 API 快照一致。请求头回写、HEAD 长度及空 builder 属公共边界，变更前需锁定兼容决定。 |
| 07 | 兼容层处置 | 严格废弃清单通过。KV 旧配置符号的保留政策仍有效，主文档推荐入口落后见 1643。 |
| 08 | 小范围可逆 | 每卡单主模块、最多五文件和三验证命令。扩展消费方取证卡归属其宿主。 |
| 09 | 命名意图 | [ValidatePhone](../../security/input/input.go#L408) 变量承载数据而实际需要数量。1641 记录计数替代和分配证据。 |
| 10 | 主流程 | [ensureServerPrepared](../../core/http_handler.go#L73) 的校验、构建和发布按顺序可读；允许重叠 TLS I/O 的所有权留给 1642。 |
| 11 | 职责与阶段 | [KV persistLocked](../../store/kv/persistence.go#L202) 及 replaceSnapshot 区分发布和清理，目录同步阶段还有独立补证。 |
| 12 | 辅助函数 | [TraceContext.Valid](../../contract/trace.go#L46) 已有验证规则。tracer 直出 span 头尚未统一，见 1637。 |
| 13 | 小接口 | [metrics](../../metrics/collector.go#L80) 分离 Recorder、HTTPObserver、StatsReader 与 Resetter，标准观测中间件仅收 HTTPObserver。 |
| 14 | 配置语义 | [KV 默认项](../../store/kv/options.go) 和 [并发限制](../../middleware/concurrencylimit/concurrency_limit.go) 各自归一化。缓存 payload 配额和日志多参数配置存在说明边界。 |
| 15 | 构造错误 | [DB OpenWith](../../store/db/sql.go#L151) 和 [auth.Authenticate](../../middleware/auth/auth.go#L52) 具错误入口。独立 Chain 的空 builder 产生运行时 panic，见 1634。 |
| 16 | 约束文档 | [日志构造器](../../log/logger.go#L86)、[健康模型](../../health/core.go#L28)、[KV Options](../../store/kv/options.go#L25) 分别核对。日志重入及默认构造说明有缺口。 |
| 17 | net/http 兼容 | [Chain](../../middleware/middleware.go#L74) 和 [Router.ServeHTTP](../../router/dispatch.go#L157) 仍使用标准签名，关键 writer 行为单独检查。 |
| 18 | 输入来源 | [请求 ID](../../middleware/requestid/helpers.go#L11) 有明确头与上下文优先级，调试路径及 SSE 协商保留分项取证。 |
| 19 | 可搜索注册 | [标准路由](../../reference/standard-service/internal/app/routes.go) 可追溯方法、路径和错误。准备失败与延迟路由选项交叠待验证。 |
| 20 | 中间件控制流 | [auth](../../middleware/auth/auth.go#L52) 拒绝后返回，Chain nil 风险已复现。正文读错误和日志收尾组合单独补证。 |
| 21 | 标准 JSON | [contract 写入入口](../../contract/response.go) 已含标准成功与错误路径，debug 使用 canonical code。payload 预览和部分头部的所属配置仍需核对。 |
| 22 | 分类错误 | [AsAPIError](../../contract/errors.go#L357) 明确忽略空指针；[DB](../../store/db/sql.go#L159) 包装主因和清理错误。未假定所有 cause 的可显示文本安全。 |
| 23 | 已提交与未提交 | [HEAD](../../router/dispatch.go#L116) 的拒绝 flush 影响长度推断。顺序文件读失败及超时继承头另有任务。 |
| 24 | writer 能力 | 新基线包含 HEAD 两层 Unwrap、204/205 和中间件组合回归。仍缺 HEAD trailers、Vary 失败回滚及结合已有业务头的证据。 |
| 25 | 敏感数据 | 查询先前改为存在标志。已检查 [正文预览](../../middleware/debug/debug_errors.go#L377)、[DB 错误](../../store/db/sql.go#L159) 和 [span 输出](../../middleware/internal/telemetry/helpers.go#L109)，实际生产配置未取证。 |
| 26 | 取消传播 | [DB 调用](../../store/db/sql.go#L255)、[JWT 轮换入口](../../security/jwt/keys.go#L105) 已接受调用者 Context。认证器替换请求、健康单项缓存、HTTP metrics fan-out 继续验证。 |
| 27 | 请求上下文 | [TraceContext](../../contract/trace.go#L55)、[Principal](../../security/authn/authn.go#L47) 均有显式访问器。请求头回写和 span 规范化具有独立所有权问题。 |
| 28 | 状态转换 | 中间件构建不再持准备锁跨回调。Goexit、Ready panic、shutdown 接管和缓存极端变化按卡处理。 |
| 29 | 资源归属 | [目录快照](../../store/kv/persistence.go#L73)、[文本日志关闭](../../log/glog_lifecycle.go#L22) 与 [连接追踪](../../core/lifecycle.go#L117) 的资源所有者已定位。原生平台运行保留上一轮 1615。 |
| 30 | 清理错误 | KV 临时关闭和移除已 join；目录关闭、数据库驱动诊断和扫描异常退出仍需针对性取证。 |
| 31 | 提交和重试 | [KV 替换](../../store/kv/persistence.go#L252) 后内存保留发布；缓存 [Append 过期分支](../../store/cache/memory.go#L404) 及只操作失效键的 Delete 需明确提交契约。 |
| 32 | 并发不变量 | race 基线通过。Additional 同名字段覆盖与同步写锁重入问题来自确定源码及独立实验，测试通过不能替代这些结论。 |
| 33 | 回调锁边界 | core 构建已在锁外。JSON 日志写入器仍在写锁内运行外部 Write，已复现同 logger 重入死锁。 |
| 34 | 可变数据所有权 | [请求头](../../middleware/requestid/helpers.go#L32) 在衍生请求共享。观测 baggage、防护头配置、字段快照及文件响应回放分别记录。 |
| 35 | 容量 | KV 已有磁盘读取预算，缓存 payload 不约束空条目，电话号码计数有可消除的分配。具体路由 miss 代价仍待基准。 |
| 36 | core 生命周期 | [公开构建回归](../../core/core_public_test.go) 和 [回滚测试](../../core/lifecycle_group_test.go) 已运行。候审卡以 TLS 重叠、异常退出和连接所有权为重点。 |
| 37 | router 匹配优先级 | 已有静态、参数、分组、URL 生成及冷热测试。新发现限于 HEAD writer。复杂通配段、Allow 扩展成本和静态读失败仍需核对。 |
| 38 | contract 传输 | 公开 [响应测试](../../contract/response_test.go) 含编码回退、短写、204/205。扩张用例聚焦应用头、多字节 request ID 和错误链。 |
| 39 | middleware 组合 | [组合回归](../../middleware/conformance/response_writer_contract_test.go) 已跑。独立 Chain、Vary、认证请求上下文及观测输出边界有候审卡。 |
| 40 | security 拒绝路径 | [JWT](../../security/jwt/verify.go) 与 [密码比较](../../security/password/password.go) 正负向关联测试已跑。Header 配置冲突有确定的源码证据，生产配置与真实时序测量未执行。 |
| 41 | store 一致性 | [KV 状态回归](../../store/kv/state_regression_test.go) 覆盖重开、读预算与清理。目录关闭、扫描异常、缓存已过期变更待证。 |
| 42 | health 状态 | [模型 JSON 测试](../../health/json_contract_test.go) 及 [健康编排回归](../../x/observability/ops/healthhttp/manual_generation_test.go) 已运行。取消缓存及不支持详情另有宿主卡。 |
| 43 | log 输出 | 既有 [安全回归](../../log/safety_regression_test.go) 和格式测试确认精度和降级。写锁重入已定位，文件 flush 生命周期和字段嵌套所有权待核对。 |
| 44 | metrics 统计 | [聚合回归](../../metrics/concurrency_regression_test.go) 支持原子计数、隔离和重置。子统计异常值、HTTP 观察上下文和新建记录归属待补证。 |
| 45 | 聚焦回归 | 新发现用公开入口隔离取证，运行记录列出 5 项对照。候审卡包含可执行测试及验收条件。 |
| 46 | 负向路径 | 认证拒绝、目录失败、TTL 路径、部分成功均有既有证据。本轮重点记录组合边界，安全项目仅做防御性源码核对。 |
| 47 | 确定性并发 | 前一轮并发回归多使用通道或合成时间，新的异步收尾和资源补证按相同方式安排。日志重入采用隔离测试超时并定位锁栈。 |
| 48 | 性能 | 本轮实验核对 ValidatePhone 每次分配数。未做改动前后比较；路由失败匹配、缓存空条目、编译策略的量化见卡。 |
| 49 | 格式和 lint | 当前代码的 `make fmt-check` 与 CI 固定版本 lint 通过，本轮审查没有格式化或编辑 Go 源码。 |
| 50 | Diff 门禁 | 代码以已提交基线执行稳定范围测试。待提交的 review 产物单独核对 docs-only profile、卡结构和链接。 |
| 51 | 覆盖率 | `make stable-coverage` 实测 96.4%。所列补证项仍按失败、提交、并发与平台分开记录。 |
| 52 | 交付同步 | 提交固定代码与上一轮证据。新报告保留 commit、代码位置、实际命令及未验证范围，新的卡留在独立候审目录。 |

## 验证记录

环境为 Go 1.26.0、darwin/arm64，Go 缓存和 lint 二进制使用临时目录。

| 命令或核验 | 结果 |
|---|---|
| `make boundary-check api-check fmt-check lint stable-coverage` | 通过。稳定快照一致，lint 0 项，稳定覆盖率 96.4%。初次脚本用到 zsh 保留变量，改名后重跑取得退出码 0。 |
| `go test -race -timeout 90s ./core/... ./router/... ./contract/... ./middleware/... ./security/... ./store/... ./health/... ./log/... ./metrics/... ./x/observability/ops/healthhttp .` | 通过。包含九根、根门面和代表健康宿主。 |
| 对九根及根门面执行 `go vet` | 通过，无诊断。 |
| 本机临时测试 `go test -v /tmp/plumego-audit-09_test.go`（加入重入用例前） | 观测到独立 Chain 空 builder panic、原请求头同步改变、HEAD 被拒绝 flush 后丢失推断长度，以及普通号码 0 次、分隔符样例 1 次分配。 |
| `go test -run '^TestRecursiveLogWriter$' -timeout 2s /tmp/plumego-audit-09_test.go` | 预计失败。2 秒超时，锁栈位于 `log/json.go:165`，外层位于相同文件的写入链。临时测试未写入仓库。 |
| `make validate-diff-dry` 与 `make validate-diff` | 通过。选中 `docs_only`，该 profile 没有附加命令。 |
| `make check-card` 与文档引用核对 | 58 张卡逐张验证通过。规则序号 01–52 连续，307 处文件链接通过。 |

本轮未运行完整 CLI、全部参考程序、网站构建及原生跨平台矩阵。尚未取证的行为留在候审卡及上一轮的 1615。
