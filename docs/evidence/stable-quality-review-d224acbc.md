# 稳定模块第 9 节质量复审

基线提交为 `d224acbc`（完成稳定模块质量复审修复和回归验证）。范围覆盖九个稳定模块和根门面，并追踪健康宿主及标准服务作为代表性调用链。

本文记录修复前的观察，基线可用 `git show d224acbc:<路径>` 查阅。源码链接定位到当前对应入口，验收证据见 [实施记录](../../tasks/cards/review-d224acbc/README.md)。

## P0–P3 发现

本轮记录 **9 项有证据的发现**，其中 3 项使用公开入口和本机桩复现。其余 45 张是待验证的契约或性能任务。本轮未确认 P0。生产配置、第三方实现的实际部署及原生平台矩阵未取证。

| 优先级 | 已复现 | 源码确认 | 补证任务 |
|---|---:|---:|---:|
| P0 | 0 | 0 | 0 |
| P1 | 3 | 1 | 0 |
| P2 | 0 | 3 | 43 |
| P3 | 0 | 2 | 2 |

证据状态标在每项后。源码确认表示能直接从当前实现追溯该路径，现场影响仍取决于列出的触发条件。

### P1

- **1743 单组件失败探测改写 Manager 持有的快照**（已复现，检查号 6,23,34,42,45）
  - 注入的 Manager 返回借用的健康快照时，失败处理同时修改状态和消息；503 已发送，调用方持有的数据也被写成 unhealthy。内置 Manager 返回副本，受影响面是自定义实现。 [x/observability/ops/healthhttp/handlers.go:118](../../x/observability/ops/healthhttp/handlers.go#L118)
  - 最小方案。以局部值构造失败响应，仅在局部值上覆盖状态和消息。
  - 验收。公开单组件入口返回原有 503 及固定错误文案，借用对象不变。 [任务卡](../../tasks/cards/done/1743-component-snapshot-ownership.md)
- **1744 单组件入口解引用存在但为空的快照**（已复现，检查号 6,20,21,42,46）
  - 合成 Manager 返回 nil、true，鲜活检查成功或失败时均 panic，端点没有形成规范的探测结果。明确未登记的分支已有 404。 [x/observability/ops/healthhttp/handlers.go:100](../../x/observability/ops/healthhttp/handlers.go#L100)
  - 最小方案。区分明确缺失和声明存在但快照无效；后者在使用前走固定 503，沿用规范错误 writer。
  - 验收。分别验证 nil,true、nil,false 和有效状态，响应只提交一次。 [任务卡](../../tasks/cards/done/1744-component-nil-snapshot.md)
- **1745 无路由告警使已经发布的 Prepare 抛出 panic**（已复现，检查号 22,28,31,33,36）
  - 应用没有路由且注入 logger 的 Warn 抛 panic 时，首次 Prepare 抛出异常，同一个 App.Server 已可取到服务器，再次 Prepare 成功。 [core/http_handler.go:110](../../core/http_handler.go#L110)
  - 最小方案。按现有关停诊断的约定隔离该告警回调的 panic，保留 handler 与服务器发布规则。
  - 验收。分别检查显式 Prepare 和直接 ServeHTTP，告警出错时正常返回且没有额外告警。 [任务卡](../../tasks/cards/done/1745-prepare-warning-after-publication.md)
- **1746 后台排水诊断无回调 panic 边界**（源码确认，检查号 29,33,36,46）
  - 排水循环在独立 goroutine 直接调用应用 logger；Info 的 panic 会逃逸该 goroutine。基线中的 Shutdown 错误日志保护不覆盖排水诊断。 [core/lifecycle.go:197](../../core/lifecycle.go#L197)
  - 最小方案。用局部保护包住单次诊断，保留 ticker 与关停退出路径。
  - 验收。合成 logger 首次 Info 抛 panic，后续排水可继续并按取消退出，重复关停可用。 [任务卡](../../tasks/cards/done/1746-drain-diagnostic-panic.md)

### P2

- **1747 初始化文件日志先删除活动链接**（源码确认，检查号 29,30,31,43）
  - 初始化逐级删除旧链接再创建，生成失败时已打开的 writer 会清理，旧链接仍可能丢失。轮换已有准备后替换的实现。 [log/glog_rotation.go:66](../../log/glog_rotation.go#L66)
  - 最小方案。首选复用 replaceLogSymlink；单独核对分级初始化失败后已发布链接指向仍可读取的文件。
  - 验收。验证旧链接、临时目录、部分初始化和重试；原生平台语义按平台验证。 [任务卡](../../tasks/cards/done/1747-init-link-publication.md)
- **1748 关闭的缓存仍保留已不可读取的全部条目**（源码确认，检查号 6,28,29,34,35）
  - Close 等待清理和写入完成，未丢弃条目。已关闭实例被应用继续持有时保留 payload。关闭后统计已被近期测试用来观察最终写入，清空需先决定兼容性。 [store/cache/memory.go:445](../../store/cache/memory.go#L445)
  - 最小方案。用受控在途读写核对关闭契约，评估屏障后解除对 payload 的持有，同时保留已承诺的终态统计。
  - 验收。验证挂起操作、重复关闭、后续拒绝及真实 payload 引用释放。 [任务卡](../../tasks/cards/done/1748-cache-closed-retention.md)
- **1749 内存 JWT KeyStore 遍历并分配过期项容量**（源码确认，检查号 14,29,35,40,48）
  - 已文档化的过期条目为保留分类而留存。KeysContext 按全量条目预分配，即使可见集合为空；重复读取会承担已过期历史的分配成本。 [security/jwt/keystore_memory.go:105](../../security/jwt/keystore_memory.go#L105)
  - 最小方案。先用固定时间和过期比例测量，考虑缩小初始容量，保持过期和未知的分类。
  - 验收。核对可见集合、取消语义与高过期比例下的 B/op。 [任务卡](../../tasks/cards/done/1749-keystore-expired-capacity.md)

### P3

- **1750 组件列表顺序随 map 遍历变化**（源码确认，检查号 6,16,34,42）
  - 列表把 map 迭代顺序写进数组，消费者无法从相同快照得到稳定展示；组件集合和 count 本身正确。 [x/observability/ops/healthhttp/handlers.go:160](../../x/observability/ops/healthhttp/handlers.go#L160)
  - 最小方案。在现有名字切片上排序，复核当前 API 对顺序没有相反要求。
  - 验收。不同注册顺序与同一快照多次调用给出相同名字顺序，空列表保持数组。 [任务卡](../../tasks/cards/done/1750-component-list-order.md)
- **1751 上一批审查报告有多余文件末尾空行**（源码确认，检查号 49,52）
  - 本次暂存差异检查报告 new blank line at EOF，提交实际保留这一行。 [docs/evidence/stable-quality-review-94305741.md:196](stable-quality-review-94305741.md#L196)
  - 最小方案。在后续独立文档变更中删除多余行。
  - 验收。比较暂存差异，保持报告正文和链接不变，git diff --check 通过。 [任务卡](../../tasks/cards/done/1751-review-eof-whitespace.md)

## 执行顺序

先完成 **1743** 的快照所有权和 **1744** 的空快照响应，再处理 **1745、1746** 两个 core 诊断回调。P2 的文件发布和容量行为在核对已有调用契约后实施。各卡的文件范围、验收和命令见 [实施索引](../../tasks/cards/review-d224acbc/README.md)。

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
| 单组件探测 | 内置实现返回已复制的模型，宿主也接受自定义 Manager | 自定义共享值被改写，存在但为空的值触发 panic。分别验收数据归属与固定失败响应。 |
| Prepare | 先发布 Server，再发一次空路由告警 | 告警回调抛 panic 后 Server 已对外可见。保留发布点，隔离诊断。 |
| Shutdown 排水 | 主调用持有单次排水槽，后台 goroutine 记录连接计数 | 现有关停错误诊断保护不涵盖后台 Info。常规取消和最后连接退出仍须保留。 |
| 文件初始化 | 每级文件打开后发布相应软链接 | 旧链接删除发生在创建新链接前；失败阶段要保住既有引用并回收已打开文件。 |
| 缓存 Close | 拒绝新请求并等待清理、写入退出，统计可读取 | 缓存保留 payload。评估释放数据和终态统计的兼容边界。 |

## 第 9 节逐项证据

| 编号 | 规则 | 本轮证据与核验限制 |
|---|---|---|
| 01 | 模块归属 | 核对九根及健康宿主清单。快照模型归 health，遇到异常自定义 Manager 的探测响应归 healthhttp（1743、1744）。 |
| 02 | 依赖方向 | 边界门禁验证稳定模块没有逆向依赖，另追踪 core 调 router，healthhttp 调 health 与 contract。 |
| 03 | 薄门面 | [根门面](../../plumego.go) 四个函数、两个别名，API 门禁核对批准集合。显式零配置比较见最后一张卡。 |
| 04 | 标准库与内部依赖 | [go.mod](../../go.mod) 未声明第三方模块。内部调用规则随边界门禁核验。 |
| 05 | 显式构造 | [参考装配](../../reference/standard-service/internal/app/app.go) 显式注入 logger。对照 Manager 的接口和内置实现，动态 nil 形态待补证。 |
| 06 | 稳定兼容 | 门禁快照匹配。缓存关闭后统计和组件列表顺序涉及现有调用习惯，分卡核对。 |
| 07 | 兼容层处置 | [废弃清单](../../specs/deprecation-inventory.yaml) 严格检查通过。既有兼容约定沿用，本次审查不迁移导出入口。 |
| 08 | 变更可逆 | 候审卡各自指定模块和范围。三项关键行为经独立本机桩确认，无生产调用或注册改动。 |
| 09 | 命名意图 | [静态读取](../../router/static.go) 的 Seek 探测和内容写入可区分。无法 rewind 的独立阶段留有补证。 |
| 10 | 主流程 | [core 准备](../../core/http_handler.go) 将验证、构建和发布分开。告警位于发布后，异常会打破返回语义（1745）。 |
| 11 | 职责与阶段 | [日志初始化与轮换](../../log/glog_rotation.go) 的资源阶段明确，初始化仍采用旧链接删除顺序（1747）。 |
| 12 | 助手封装 | 日志轮换已有 replaceLogSymlink 可复用。健康探测已有固定错误 writer 与模型 Clone。 |
| 13 | 小接口 | [healthhttp.Manager](../../x/observability/ops/healthhttp/manager.go) 组合组件注册和检查职责。宿主应处理接口返回的对象所有权；metrics 的 Recorder 与 StatsReader 保持分离。 |
| 14 | 配置语义 | [并发限制](../../middleware/concurrencylimit/concurrency_limit.go) 规范化深度和等待时间。builder 复用的配额范围待证。 |
| 15 | 构造失败 | [DB](../../store/db/sql.go) 提供可返回错误的 OpenWith。core 依赖适配支持正常 logger。自定义 Manager 的动态 nil 与有效零状态待证。 |
| 16 | 约束文档 | [静态文件](../../router/static.go) 已限定可信根；[内存 KeyStore](../../security/jwt/keystore_memory.go) 记录过期条目继续留存。组件列表顺序当前缺少约定。 |
| 17 | net/http 兼容 | [Router](../../router/dispatch.go) 和 [Chain](../../middleware/middleware.go) 使用标准签名。实际 writer 行为按 23、24、39 核验。 |
| 18 | 输入来源 | [请求 ID 解码](../../middleware/requestid/generator.go) 和 [静态路径](../../router/static.go) 的入口清楚。精确整数边界与表示组合待补证。 |
| 19 | 可搜索注册 | [标准路由](../../reference/standard-service/internal/app/routes.go) 可跟随方法、路径、handler 和错误返回。冻结后先直接调用再准备 TLS 的顺序另有取证。 |
| 20 | 中间件控制流 | [并发限制](../../middleware/concurrencylimit/concurrency_limit.go) 获取许可后重新检查取消。正文 reader 与响应提交之间的取消仍待验证。 |
| 21 | 规范 JSON | [contract 入口](../../contract/response.go) 统一 writer。健康探测空组件异常未形成规范响应（1744）。 |
| 22 | 错误分类 | [DB 包装](../../store/db/sql.go) 保留延迟扫描与迭代的底层契约，跨结果集和 lazy 错误另证；告警 panic 改变 Prepare 返回（1745）。 |
| 23 | 提交边界 | [HEAD writer](../../router/dispatch.go) 延迟提交；Debug 选状态后的刷新与 CSV 短写仍需核对。 |
| 24 | writer 能力 | [中间件组合测试](../../middleware/conformance/response_writer_contract_test.go) 随 race 运行；HEAD 接管、304 与 Range 同场景和短写保头留有卡片。 |
| 25 | 敏感数据 | 核对 [认证](../../security/authn/token.go)、[安全头验证](../../security/headers/headers.go) 与 [规范错误 writer](../../contract/response.go)。只使用人工配置，未核验部署凭据和日志采样。 |
| 26 | 取消传播 | [验证入口](../../security/jwt/verify.go) 与 [缓存写屏障](../../store/cache/memory.go) 检查上下文。对照前后边界及 tracer 派生取消需要强化。 |
| 27 | 上下文范围 | [contract trace](../../contract/trace.go) 和 [request ID](../../contract/request_id.go) 显式存储。多份 baggage 与 trace flags、响应 request ID 优先级分别验证。 |
| 28 | 状态转换 | Prepare 先发布再告警。cache Close 后数据不再可读但仍可统计。Ready 的 Goexit 与 TLS 冻结后准备均在候审。 |
| 29 | 资源所有权 | 排水 ticker、缓存写屏障、日志活动链接和静态文件 Close 路径已追踪。平台语义保留原生验证限制。 |
| 30 | 清理错误 | [日志初始化](../../log/glog_rotation.go) 清理已打开的文件但没有恢复原链接。DB 现有 close 错误回归随测试执行。 |
| 31 | 提交和重试 | Prepare 的 warn panic 发生在 Server 发布后，公开重试已复现；日志 link 替换与 KV 读预算重试另有明确边界。 |
| 32 | 并发不变量 | 稳定范围 race 通过。排水末连接、路由缓存逐出和聚合快照读取顺序需受控交错。 |
| 33 | 回调边界 | [排水诊断](../../core/lifecycle.go) 的 Info panic 缺隔离，上一批仅保护关停错误日志（1746）。 |
| 34 | 可变数据归属 | 内置 Manager 防御复制。注入实现可返回借用对象，单组件失败改写已复现（1743）。不同视图、文件 metadata 和缓存终态另证。 |
| 35 | 容量 | cache Close 保留 payload，KeyStore Keys 当前按全部历史项预分配；注入时钟同步扫描成本和指标重置留给定向测量。 |
| 36 | core 生命周期 | [生命周期回归](../../core/lifecycle_group_test.go) 已运行。新发现限于诊断回调和发布语义；HTTP/2 真实协商另证。 |
| 37 | router 匹配 | [冷热矩阵](../../router/router_contract_test.go) 随 race 运行。HEAD 接管、逐出后在途 RouteState 和 rewind 被列入专项。 |
| 38 | contract 传输 | [公开 writer 测试](../../contract/response_test.go) 已执行。待补证 request_id 优先级、业务头短写和错误状态边界。 |
| 39 | middleware 组合 | 当前正文、timeout、CORS、恢复和压缩复合测试通过。进一步核对 permit 共享、响应前取消和 Flush 后先前状态。 |
| 40 | security 拒绝路径 | [JWT 验证](../../security/jwt/verify.go) 和密码组合测试已运行，静态认证比较采用 timing-safe 原语。生产策略、真实时间侧信道未核验。 |
| 41 | store 一致性 | [KV 状态回归](../../store/kv/state_regression_test.go) 随 race 运行。缺字段与历史空对象、缓存取消和 DB 多结果集需定向证据。 |
| 42 | health 状态 | [稳定模型](../../health/core.go) 对状态的现有测试通过。异常宿主结果影响失败处理和对象所有权，内置调度无新运行结论。 |
| 43 | log 输出 | [JSON](../../log/json_test.go) 包含精度、失败回退和字段隔离测试；新风险在活动链接初始化和 Flush 时序。 |
| 44 | metrics 一致性 | [并发测试](../../metrics/concurrency_regression_test.go) 通过。独立子快照约定沿用，最早时间、回调失败和重置成本待补证。 |
| 45 | 聚焦回归 | 三项异常以公开入口和本机合成依赖观测。任务卡给出对应位置、可观察验收和验证命令。 |
| 46 | 负向路径 | 已实测空健康快照、错误探测及 panic 日志回调。后台诊断结论根据源码，不执行可能终止宿主的试验。 |
| 47 | 确定性并发 | 当前 race 通过。排水 tick、生命周期阶段及锁等待建议使用通道或合成时钟，候审卡尚未实施。 |
| 48 | 可重复度量 | 本轮无性能变化结论。过期比例、注入钟扫描和高基数清空给出计划基准，未来采样后再决定优化。 |
| 49 | 格式和 lint | Go 格式和 lint 门禁见验证表。已提交的旧报告有末尾空行，新候审文件单独校验。 |
| 50 | Diff 门禁 | 基线跑稳定范围 Go 门禁，新报告与卡片按 docs_only 配置核验。 |
| 51 | 覆盖率 | 聚合覆盖见验证表。回调失败、清理屏障、平台差异和 HTTP 提交都按风险额外记证。 |
| 52 | 同步和交付 | 记录实际基线、源码行号、验证命令、补证范围和上一轮未完成的平台卡。本轮未改运行契约。 |

## 复现记录

| 本机测试 | 观察结果 |
|---|---|
| `TestHealthOwnedState` | 自定义 Manager 返回共享组件对象，模拟鲜活检查失败。响应 503，借用对象的状态和消息也变成失败。内置实现返回副本。 |
| `TestHealthNilState` | 自定义 Manager 返回空快照并声明组件存在。无论鲜活检查成功还是失败，handler 均出现 nil 解引用。 |
| `TestPrepareWarningAfterPublication` | 空路由 App 的合成 logger 在 Warn 抛 panic。第一次 Prepare 抛 panic，Server 已可取，第二次 Prepare 返回成功。 |

## 验证记录

| 命令 | 结果 |
|---|---|
| `make boundary-check api-check fmt-check lint stable-coverage` | 通过，九根快照匹配，lint 0 项，稳定模块覆盖率 96.6%。 |
| `go test -race -timeout 90s ./core/... ./router/... ./contract/... ./middleware/... ./security/... ./store/... ./health/... ./log/... ./metrics/... ./x/observability/ops/healthhttp .` | 通过。 |
| 对同一范围执行 `go vet` | 通过。 |
| `go test -v /tmp/plumego-d224_test.go` | 三项基线行为均被观察到。临时桩留在本机。 |
| `git show --format= --check HEAD` | 检出旧审查报告第 197 行的多余空行，留给 1751。 |
| `make validate-diff-dry`、`make validate-diff` | 通过，选择 `docs_only`，该配置没有额外命令。 |
| 逐张 `make check-card C=review-d224acbc/<卡片文件名>`，同时核对编号、链接及行号 | 54 张全部通过。52 个检查号、286 条链接及源码定位均已校验。工作区只新增本批 56 个 Markdown 文件。 |

本轮未运行完整参考模块、CLI、网站构建、生产部署和原生跨平台矩阵。
