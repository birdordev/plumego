# 稳定模块第 9 节质量复审

基线为本地提交 `2f962d21`（完成 HEAD 响应、日志轮换与指标边界复审修复）。覆盖九个稳定模块、根门面及健康宿主和标准服务的相关调用链。 [质量规则第 9 节](../operations/agent-code-quality-rules.md#L267)

## 基线 P0–P3 发现

确认 **6 项**，其中 3 项有本机复现，3 项为源码确认的文档缺口。另有 49 张定向补证卡。

| 优先级 | 已复现 | 源码确认 | 补证任务 |
|---|---:|---:|---:|
| P0 | 0 | 0 | 0 |
| P1 | 1 | 0 | 0 |
| P2 | 2 | 0 | 33 |
| P3 | 0 | 3 | 16 |

### P0

本轮未确认此级别问题。生产配置及部署可达性未纳入取证。

### P1

- **2064 批次发布在管理器锁内调用上下文方法**（已复现，检查号 26,32,33,42,45,47）
  - 证据与影响。空组件批次在最终发布段持有写锁调用传入的 ctx.Err。同包探针观察到读锁不可用，走公开接口的隔离进程中，Err 在读取 GetConfig 时互等至 2 秒测试超时。标准库上下文路径未观察到该阻塞。 [x/observability/ops/healthhttp/manager.go:378](../../x/observability/ops/healthhttp/manager.go#L378)
  - 最小方案。在锁外读取上下文信号和错误。发布持锁时只查看已取得的信号、版本和关闭状态。若取消刚到而错误类型尚未读取，释放锁采样错误，再重新校验发布资格。
  - 需要补齐的回归。受控上下文在 Err 中读取配置后，空批次可正常结束。用屏障分别核验取消类型、较新批次、手动就绪和关闭仍拥有各自的发布优先级。 [任务卡](../../tasks/cards/done/2064-aggregate-context-callback-outside-manager-lock.md)

### P2

- **2065 归档修改时间并列时保留较早世代**（已复现，检查号 14,29,35,43,45）
  - 证据与影响。同一程序、主机、级别有三份轮换文件，文件名的分秒和纳秒可区分世代，磁盘修改时间被设为同值。保留一份时留下最早命名世代，后两份都被清理。真实文件系统发生频率未取证。 [log/glog_rotation.go:296](../../log/glog_rotation.go#L296)
  - 最小方案。保持修改时间为主排序键。同值时为同系列已识别的文件名定义后续世代优先规则，并明确不同系列或历史命名的确定性兜底。
  - 需要补齐的回归。同修改时间两代以上留下预期世代。追加不同时刻与原活动候选样例，分别验证主排序及活动保护继续有效。 [任务卡](../../tasks/cards/done/2065-retention-tied-mtime-keeps-newer-generation.md)
- **2066 健康报告可能同时列出两个注册世代**（已复现，检查号 16,32,34,42,45）
  - 证据与影响。两轮均用受控 Manager 在第一次读取后注销组件，报告同时得到旧 Components 和无该组件的整体快照。内置路径也按三个公共读方法分别上锁，实际并发出现频率未测。 [x/observability/ops/healthhttp/tracker.go:562](../../x/observability/ops/healthhttp/tracker.go#L562)
  - 最小方案。为内置 manager 提供私有报告快照，在同一读临界区复制整体状态及组件名，并按现有锁顺序取就绪发布值。外部 Manager 继续按已有接口读取，并说明其分次读限制。
  - 需要补齐的回归。使用组件替换时序验证内置报告组件名与整体依赖来自同一注册表快照。保留既有就绪快照、关闭时的报告形状和第三方 Manager 回退。 [任务卡](../../tasks/cards/done/2066-report-built-in-manager-coherent-snapshot.md)

### P3

- **2067 说明空切片 Append 的归一化**（源码确认，检查号 6,14,16,34,41）
  - 证据与影响。现有回归要求缺失键接收显式空切片时创建 nil 值。公开 Append 说明以 data 为值，容易使使用者类推 Set 保留显式空切片的行为。公开调用对照确认两者返回的形状不同。 [store/cache/memory.go:389](../../store/cache/memory.go#L389)
  - 最小方案。在 Append 的 GoDoc 明确空追加在缺失或已过期键上创建 nil 值，在活跃键上保持原值。沿用当前默认 TTL 与可见形状。
  - 需要补齐的回归。复用并补充 nil 和显式空值组合，从 Append、Get、Set 公开入口确认形状、计量及兼容。 [任务卡](../../tasks/cards/done/2067-describe-cache-empty-append-normalization.md)
- **2068 明确事务回调的提交与回滚所有权**（源码确认，检查号 16,22,29,31,41）
  - 证据与影响。WithTransaction 根据回调返回值调用 Commit 或 Rollback。回调接收可自行结束事务的 *sql.Tx，若已提交，外层再次 Commit 只看到 sql.ErrTxDone，不能据此判断实际提交结果。公开说明未提醒调用方。 [store/db/sql.go:258](../../store/db/sql.go#L258)
  - 最小方案。注明回调负责查询和写入，结束事务由 helper 负责。保留现有错误链和分类，避免把 ErrTxDone 一概当成功。
  - 需要补齐的回归。用现有事务桩记录手动结束后外层的分类与驱动操作次数，另确认普通回调完成继续提交一次。 [任务卡](../../tasks/cards/done/2068-document-withtransaction-finalization-ownership.md)
- **2069 说明关闭后的批次历史与尝试计量**（源码确认，检查号 16,28,31,42,52）
  - 证据与影响。上轮受控探针确认关闭后再调用批检查增加一条不可用历史，回调和计数保持原值。公开注释只描述执行检查，缺少零次尝试的历史口径。 [x/observability/ops/healthhttp/tracker.go:253](../../x/observability/ops/healthhttp/tracker.go#L253)
  - 最小方案。在入口 GoDoc 明确 Tracker 记录批调用终态和实际组件尝试，两者有独立计量时点。
  - 需要补齐的回归。先完成一批再关闭并发起第二批，核对历史、组件调用数、LastCheckTime 与 readiness，保留现有语义。 [任务卡](../../tasks/cards/done/2069-document-tracker-closed-history-accounting.md)

## 基线执行顺序

先做 **2064** 的批次发布锁问题，随后做 **2065** 的归档同时间保留、**2066** 的内置健康报告快照及 **2115** 的取消竞争边界。2065 可独立验证。2115 等待 2064，2116 和 2118 等待 2066。 [55 张任务卡的优先级与依赖](../../tasks/cards/review-2f962d21/README.md)

| 实施风险 | 验收约束 |
|---|---|
| 移动上下文方法可能改变取消与发布的竞争结果 | 用 2115 核对取消类型、关闭和更新批次的优先级。 |
| 并列排序改变活跃归档的保留 | 用 2108 验证级别、旧文件名和正在使用的链接。 |
| 共同读取抹平手动和观察到的不同状态 | 用 2116、2118 验证手动就绪与健康的独立语义以及快照所有权。 |

## 模块取证

使用 `stable_root_boundary_review` 与 review 上下文包。已读九个稳定模块清单、根门面约束和健康宿主清单。边界门禁和稳定 API 快照通过。源码审查及验证依据如下。

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
| [metrics](../../metrics/module.yaml) | [基础记录和快照](../../metrics/collector.go)、[聚合](../../metrics/multi.go) | [HTTP 适配](../../middleware/httpmetrics/http_metrics.go)、[聚合回归](../../metrics/multi_test.go) |
| 根门面 | [New、配置与 Param](../../plumego.go) | [公开门面测试](../../plumego_test.go) |

健康报告组件顺序、历史与尝试计量均由 [健康宿主](../../x/observability/ops/module.yaml) 承担。

## 第 9 节逐项证据

| 检查号 | 证据状态 | 核验说明与限制 | 代码位置 |
|---|---|---|---|
| 01 | 边界核验 | 九个稳定根和健康宿主清单已核对。报告读取与批次发布归健康宿主，缓存与事务说明归各存储子包。 | [module.yaml:12](../../x/observability/ops/module.yaml#L12) |
| 02 | 边界门禁 | 边界检测通过，包括稳定根禁用的上层依赖与内部 helper 的调用方约束。 | [dependency-rules.yaml:10](../../specs/dependency-rules.yaml#L10) |
| 03 | 边界门禁 | 根门面导出快照匹配。2114 补关闭后直接分派与公开参数入口的组合。 | [plumego.go:60](../../plumego.go#L60) |
| 04 | 依赖核验 | 根模块保持标准库，边界检查包含模块和内部引用约束。报告增量没有 Go 依赖。 | [go.mod:3](../../go.mod#L3) |
| 05 | 源码取证 | 构造依赖从字段传入，标准服务负责装配。2071 验证中间件构建异常能归还装配所有权。 | [dependencies.go:8](../../core/dependencies.go#L8) |
| 06 | 源码与回归 | Set 保留显式空值，Append 在新键归一化成 nil，既有测试明确要求。2067 保留行为并更正注释。 | [memory.go:393](../../store/cache/memory.go#L393) |
| 07 | 边界核验 | 严格清单门禁通过。抽查 KV 的 Options 与默认构造兼容层，未统计仓库外调用。 | [deprecation-inventory.yaml:7](../../specs/deprecation-inventory.yaml#L7) |
| 08 | 计划核验 | 55 卡分别指定归属、源代码位置、验收与依赖，包含复现、源码确认和待补证。 | [README.md:3](../../tasks/cards/review-2f962d21/README.md#L3) |
| 09 | 源码取证 | 名称标记批次职责，问题在当前临界区划分。2064 保持入口和错误名。 | [manager.go:344](../../x/observability/ops/healthhttp/manager.go#L344) |
| 10 | 源码取证 | BeginTx、回调和 Commit 分层次处理，2068 补结束事务的责任和类型分类。 | [sql.go:265](../../store/db/sql.go#L265) |
| 11 | 已复现 | 读取快照、年龄清理和数量淘汰分段。具名世代在相同修改时间下比较失效，2065 仅改保留排序。 | [glog_rotation.go:206](../../log/glog_rotation.go#L206) |
| 12 | 已复现 | 多读路径能组装两个注册表快照。2066 将内置 Manager 相关部分收拢到私有读取，保留外部接口。 | [tracker.go:548](../../x/observability/ops/healthhttp/tracker.go#L548) |
| 13 | 源码与待补证 | 快照写入依赖四方法窄接口，正式调用来自 os.File。2099 取证非标准注入短写。 | [persistence.go:238](../../store/kv/persistence.go#L238) |
| 14 | 源码与待补证 | 缓存明确正 TTL 和默认 TTL。2100 验证在活跃显式空值上空追加的期限。 | [memory.go:256](../../store/cache/memory.go#L256) |
| 15 | 源码与现有回归 | 注入构造能分类配置和打开错误，部分打开的 pool 由 helper 关闭。外部实际驱动未连接。 | [sql.go:152](../../store/db/sql.go#L152) |
| 16 | 源码确认 | *sql.Tx 结束操作需要文档明确归属，2068 补规则。缓存和 Tracker 的兼容说明见 2067、2069。 | [sql.go:258](../../store/db/sql.go#L258) |
| 17 | 源码与回环回归 | net/http 标准签名。已有空 HEAD 长度回归通过，2076 补信息响应和 Flush 拒绝的组合。 | [dispatch.go:159](../../router/dispatch.go#L159) |
| 18 | 源码与待补证 | 反向路由逐段转义路径输入，2077 确定普通百分号与非 ASCII 字面字符往返。 | [metadata.go:113](../../router/metadata.go#L113) |
| 19 | 源码取证 | 标准服务在装配中把方法、路径和 handler 连同错误集中处理，2072 补 Any 的命名元数据。 | [routes.go:16](../../reference/standard-service/internal/app/routes.go#L16) |
| 20 | 源码与待补证 | 提前返回区分普通 OPTIONS 与预检，2087 验证两条正常入口的 next 次数。 | [cors.go:151](../../middleware/cors/cors.go#L151) |
| 21 | 源码与现有回归 | 成功和错误走各自统一入口。2079 将失败编码和后续带请求 ID 的错误响应放在同一写入者验证。 | [response.go:56](../../contract/response.go#L56) |
| 22 | 待补证 | 保留 begin、回调和驱动失败类型。2068 验证回调自行终止时 ErrTxDone 分类。 | [sql.go:265](../../store/db/sql.go#L265) |
| 23 | 待补证 | 先确定已有最终状态与能力，再决定提交。2076、2083、2089 核查信息头、Flush 和恢复组合。 | [dispatch.go:116](../../router/dispatch.go#L116) |
| 24 | 待补证 | 已核对 1xx 和最终状态分支。2084 使用回环验证接续 304 的真实网络结果。 | [gzip.go:232](../../middleware/compression/gzip.go#L232) |
| 25 | 源码与验证范围 | 检查固定诊断和 panic 分类。2089 用普通数据测试网络阶段，生产记录与真实凭据未取证。 | [recover.go:83](../../middleware/recovery/recover.go#L83) |
| 26 | 已复现 | 组件批次在最终持锁路径调用自定义 Err，可因只读重入触发阻塞。2064 与 2115 回归应覆盖取消优先级。 | [manager.go:378](../../x/observability/ops/healthhttp/manager.go#L378) |
| 27 | 源码与待补证 | Context 键私有，请求 ID 的 accessor 规则由 2081 增补边界。 | [request_id.go:9](../../contract/request_id.go#L9) |
| 28 | 源码与现有回归 | 失败启动滚回且推进阶段。2070 补冻结后的追加行为，2073 补连接状态计量。 | [lifecycle_group.go:134](../../core/lifecycle_group.go#L134) |
| 29 | 源码与待补证 | 延迟关闭负责 SQL 结果，2102 观察回调 Goexit 后的连接回收。实际驱动差异未测。 | [sql.go:386](../../store/db/sql.go#L386) |
| 30 | 源码与现有回归 | 保留写入和关闭主次错误，已有联合错误与重试证据。2099 单独隔离非标准短写假设。 | [persistence.go:270](../../store/kv/persistence.go#L270) |
| 31 | 源码与待补证 | 启动内存裁剪独立于磁盘提交。2097 验证打开、纯读、下一次删除与重新打开顺序。 | [kv.go:103](../../store/kv/kv.go#L103) |
| 32 | 已复现 | 受控注册变更导致同一报告列表与整体依赖不一致。外部接口无法提供共同读取点，2066 单独处理内置能力。 | [tracker.go:562](../../x/observability/ops/healthhttp/tracker.go#L562) |
| 33 | 已复现 | 锁内自定义上下文回调的实际重入被隔离进程超时捕获，调用栈落在 GetConfig 读锁。 | [manager.go:378](../../x/observability/ops/healthhttp/manager.go#L378) |
| 34 | 源码与待补证 | 健康模型 Clone 仅复制容器。2106 保留已声明的嵌套共享和顶层隔离。 | [core.go:45](../../health/core.go#L45) |
| 35 | 已复现 | 归档数量有界，修改时间并列时优先保留的世代缺少规则。2108 约束活跃链接以及独立级别。 | [glog_rotation.go:296](../../log/glog_rotation.go#L296) |
| 36 | 源码与现有回归 | 已有准备、回滚和关闭回归。2070–2073 扩展失败冻结、构建 panic、Any 元数据和排空观察。 | [http_handler.go:53](../../core/http_handler.go#L53) |
| 37 | 源码与现有回归 | 路由缓存定向验证通过。2078 补热 Any 的失效和各方法的 Allow 刷新。 | [registration.go:195](../../router/registration.go#L195) |
| 38 | 源码与现有回归 | 先编码再提交，bodyless 提前处理。2079–2082 补重试、具名详情和访问器等分支。 | [response.go:22](../../contract/response.go#L22) |
| 39 | 源码与现有回归 | 链条 builder 在显式装配中保持次序。2083–2091 补网络状态、HTTP 能力与快照关联。 | [middleware.go:129](../../middleware/middleware.go#L129) |
| 40 | 源码与现有回归 | 确认现有标准库时序安全比较和拒绝测试。2092–2096 侧重配置、存储和快照边界，未验证部署策略。 | [token.go:72](../../security/authn/token.go#L72) |
| 41 | 源码与现有回归 | 当前快照写入经过临时文件再替换，裁剪、过期、故障和重开继续以 2097–2104 验收。 | [persistence.go:245](../../store/kv/persistence.go#L245) |
| 42 | 已复现 | 批次锁边界与报告快照发现见 2064、2066。既有陈旧结果、关闭和手动状态回归通过。 | [manager.go:344](../../x/observability/ops/healthhttp/manager.go#L344) |
| 43 | 已复现 | 受控并列时间下较新归档被清理。JSON 精度、文本输出通过现有测试，2108–2110 补组合。 | [glog_rotation.go:206](../../log/glog_rotation.go#L206) |
| 44 | 源码与现有回归 | 饱和计数回归通过。2111–2113 分别补子重置时间、空名称与 HTTP 分发所有权。 | [collector.go:145](../../metrics/collector.go#L145) |
| 45 | 现有回归与探针 | 既有轮换同名修复回归通过。本轮三项行为卡以公开路径或窄 backend 接口建立可重复验收。 | [glog_test.go:1826](../../log/glog_test.go#L1826) |
| 46 | 待补证 | 2101 结合解码失败、容量与过期验证可恢复性，其余异常路径按具体任务验收。 | [memory.go:343](../../store/cache/memory.go#L343) |
| 47 | 现有回归与探针 | 用同步信号和只读探针固定到达点，测试超时仅作为隔离进程的阻塞出口。 | [aggregate_regression_test.go:229](../../x/observability/ops/healthhttp/aggregate_regression_test.go#L229) |
| 48 | 范围说明 | 现有性能入口已定位，本轮未声明吞吐、内存或保留频率改善，未重跑基准。 | [router_bench_test.go:37](../../router/router_bench_test.go#L37) |
| 49 | 独立检查 | 格式、lint 通过，工作区审查增量不触碰 Go 文件。 | [Makefile:28](../../Makefile#L28) |
| 50 | 门禁选择 | 报告和卡片走 docs_only，另对提交基线执行稳定包 race、vet、边界和 API。 | [Makefile:22](../../Makefile#L22) |
| 51 | 门禁与限制 | 覆盖率 96.8%。文件时间碰撞频率、真实外部 Manager 时序和生产部署仍属验证缺口。 | [Makefile:78](../../Makefile#L78) |
| 52 | 交付记录 | 上一轮已提交，本轮按已复现、源码确认及补证记录结果。公开 API 未改。 | [stable-quality-review-c3a77410.md:166](stable-quality-review-c3a77410.md#L166) |

## 本机复现

| 临时探针 | 观察结果 |
|---|---|
| `TestReviewBatchContextCalledUnderManagerLock` | 空批次的自定义 Err 执行时，同包 TryRLock 返回 false。空批次正常上下文路径仍返回 ready。 |
| `TestReviewReadOnlyReentryDuringBatch` | 公共入口调用超时。2 秒时的栈从 checkBatch → Err → GetConfig，阻塞在管理器 RLock。 |
| `TestReviewRetentionKeepsEarlierTimestampOnTiedMtime` | 三份归档相同修改时间，按纳秒区分文件名。保留一份后最早世代在，后两世代已删除。 |
| `TestReviewReportMayMixManagerSnapshots` | 报告 Components 留有 fixture，而 HealthStatus.Dependencies 无该组件。注入 Manager 在列表读取后触发注销。 |
| `TestReviewAppendNormalizesNewEmptySlice` | Set 保留非 nil 空切片，Append 在新键得到 nil。既有公开测试已固定此差异。 |

探针使用本机临时文件与普通占位数据。同包观察用 Go overlay，阻塞路径在独立测试进程内由超时退出。并列时间的生产出现频率和外部管理器的实际时序未测。

## 基线验证记录

| 命令 | 结果 |
|---|---|
| `git show --format= --check HEAD` | 通过，基线 2f962d21。 |
| `go test -race -count=1 -timeout 60s ./core ./router ./contract ./middleware/... ./security/... ./store/... ./health ./log ./metrics ./x/observability/ops/healthhttp .` | 通过，禁用结果缓存重跑。 |
| 对同一包集合运行 `go vet` | 通过。 |
| `make boundary-check api-check fmt-check lint stable-coverage` | 通过，稳定 API 快照匹配，lint 0 项，稳定语句覆盖率 96.8%。 |
| `go test -race -count=1 -v -overlay=/tmp/2f9-overlay.json ./log ./x/observability/ops/healthhttp -run ^TestReview` | 日志与健康观察探针通过。 |
| `go test -race -count=1 -v /tmp/2f9-public_test.go` | 通过，确认 Append 和 Set 的值形状差异。 |
| `go test -timeout=2s -count=1 -run ^TestReviewReadOnlyReentryDuringBatch$ /tmp/2f9-health-reenter_test.go` | 如预期超时，确认公开路径重入阻塞，待 2064 修复后应通过。 |
| `make validate-diff-dry` 和 `make validate-diff` | 选择 docs_only 并通过，该配置无额外运行命令。 |
| 逐卡 `make check-card` 与交付核验 | 55 张卡、52 个检查号、348 条本地链接及源码行号通过，工作区仅新增 57 个 Markdown 文件。 |

本轮未重跑参考模块全量、CLI 全量、网站构建、原生跨平台与基准，也未连接生产依赖。审查阶段只新增报告与候审卡。

## 实施结果

2064–2118 已完成。三个修复回归在原生产代码上均失败，修复后通过。48 张定向补证卡执行了新增、增强或已有等价回归。 [实施索引](../../tasks/cards/review-2f962d21/README.md)

| 卡片 | 实施结果 |
|---|---|
| 2064、2115 | 批次在锁外读取上下文方法。锁内根据取消信号和发布资格作决定，刷新取消错误后重查版本与关闭。标准和自定义上下文的分类均保留。 |
| 2065、2108 | 先比较文件修改时间。同值时比较归档文件名中的完整时间戳和可选秒小数，仍同值时按文件名降序。现代和旧式归档都参与排序，活动文件保护保持。 |
| 2066、2116、2118 | 内置报告读取同一组件表，按既有获锁顺序同时取得就绪快照，组件名与依赖列表分别归调用方。第三方 Manager 的分次读取和 Tracker 指标的独立快照保持。 |
| 2067–2069 | 补齐空追加 nil 形状、事务结束所有权和关闭后的批次历史口径，保留原行为。 |
| 2093、2094 | 核验 HSTS 和密码强度的零值。HSTS 零值省略生成头，纠正原补证卡预期。密码强度零配置无要求，文档明确调用方可显式选择默认策略。 |
| 2099 | 非标准 writer 返回成功短写会发布截断文件，真实 os.File 的短写按其契约返回错误。本卡仅补证，正式持久路径无修改。 |

### 实施门禁

| 命令 | 结果 |
|---|---|
| `go test -race -count=1 -timeout 25s -overlay=/tmp/2f9-impl-old-overlay.json ./log ./x/observability/ops/healthhttp`（筛选三项修复回归） | 三组在旧生产代码上按预期失败。 |
| 稳定模块、根门面与健康宿主的定向 `go test -race -timeout 60s` | 通过。 |
| `go test -race -timeout 60s -count=10 ./log ./x/observability/ops/healthhttp ./core`（筛选七项修复及关联边界） | 七项均连续十次通过。 |
| `go test -timeout=2s -count=1 -run ^TestReviewReadOnlyReentryDuringBatch$ /tmp/2f9-health-reenter_test.go` | 公开重入探针在修复后完成。 |
| `go test -race -count=1 -v -overlay=/tmp/2f9-impl-kv-overlay.json ./store/kv -run ^TestReviewInjectedSnapshotShortWritePublishesPrefix$` | 通过，记录非标准短写替身观察。 |
| `make fmt`、`make test`、`make lint`、`make stable-coverage` | 通过，lint 0 项，稳定语句覆盖率 96.8%。 |
| `make website-sync`、`make website-gates` | 通过，生成数据无差异，仍有既有 `:global` CSS 压缩警告。 |
| 逐卡 `make check-card`、`git diff --check` | 通过，55 张归档卡、52 个检查号、418 条本地链接与源码行号均通过。 |
| `make validate-diff-dry`、`make validate-diff` | 通过，选择 cross_module_public_api_release，执行完整 `make gates`。 |

真实归档时间碰撞频率、外部 Manager 的生产时序与原生跨平台未测量。
