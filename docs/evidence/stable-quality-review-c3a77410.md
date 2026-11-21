# 稳定模块第 9 节质量复审

基线为本地提交 `c3a77410`（完成健康检查与响应尾字段复审修复）。覆盖九个稳定模块、根门面及健康宿主和标准服务的相关调用链。 [质量规则第 9 节](../operations/agent-code-quality-rules.md#L267)

## 基线 P0–P3 发现

确认 **6 项**，其中 4 项通过本机行为探针复现，2 项由源码确认。另有 50 张针对具体行为与取证限制的补证卡。

| 优先级 | 已复现 | 源码确认 | 补证任务 |
|---|---:|---:|---:|
| P0 | 0 | 0 | 0 |
| P1 | 0 | 0 | 0 |
| P2 | 4 | 0 | 36 |
| P3 | 0 | 2 | 14 |

### P0

本轮未确认此级别问题。生产配置及部署可达性未纳入取证。

### P1

本轮未确认此级别问题。已复现偏差的影响范围见 P2。

### P2

- **2008 空 HEAD 响应自动声明零长度**（已复现，检查号 6,17,24,37,45）
  - 证据与影响。在回环 HTTP 服务中，显式返回 202 且没有写响应体的处理器经 Router 后收到 Content-Length: 0，直接交给标准库时该头缺失。零可能被客户端解释为对应表示确知为空。 [router/dispatch.go:91](../../router/dispatch.go#L91)
  - 最小方案。为 HEAD 的自动长度推断增加实际写入正字节条件，保留显式 Content-Length、正常小响应推断和现有无正文状态规则。
  - 需要补齐的回归。并排跑原生与 Router 的显式及隐式空 HEAD，断言状态、头和空正文，再验证写入正字节的路径。 [任务卡](../../tasks/cards/done/2008-head-empty-handler-length-parity.md)
- **2009 固定时刻下轮换复用活动文件**（已复现，检查号 14,28,29,31,35,43,45）
  - 证据与影响。在固定时钟下初始化同一个私有文件 logger，再通过 Info 写入两条各 1 MiB 的记录。活动路径从未切换，文件已有两条记录，计量却归零。真实时钟碰撞频率未测。 [log/glog_rotation.go:143](../../log/glog_rotation.go#L143)
  - 最小方案。轮换仅在成功创建独立新文件后切换 writer 并清零，遇同名选用可被保留逻辑识别的候选名称。兼容既有文件名与链接更新次序。
  - 需要补齐的回归。同一固定时刻连续越阈值，验证活动路径单独切换、已归档内容完整且新活动文件计量准确。 [任务卡](../../tasks/cards/done/2009-rotation-distinct-target-at-equal-clock.md)
- **2010 基础统计越上界后返回负值**（已复现，检查号 6,32,44,45）
  - 证据与影响。将同包测试计数预置到 int64 上限，再用公开 Record 记录一项失败。直接和聚合统计的总数、错误数与名称计数均回绕为最小负值，违反非负契约。生产到达上界的概率未取证。 [metrics/collector.go:150](../../metrics/collector.go#L150)
  - 最小方案。在同一锁内为三处计数复用聚合的饱和相加，保留 Clear 和状态分类。
  - 需要补齐的回归。验证上限前后非负、错误数不超过总数、另外一个名字仍可增长，清空后重新从零计数。 [任务卡](../../tasks/cards/done/2010-base-nonnegative-saturated-counter.md)
- **2011 健康报告组件列表顺序漂移**（已复现，检查号 6,16,34,42,45）
  - 证据与影响。固定组件集合和管理器快照后，连续 GenerateReport 返回不同组件列表排列。调用方按序列对比时出现无状态变化的差异。 [x/observability/ops/healthhttp/tracker.go:558](../../x/observability/ops/healthhttp/tracker.go#L558)
  - 最小方案。在报告组装处排序组件名，与健康批次的组件顺序对齐。公开字段形状保持原样。
  - 需要补齐的回归。对乱序注册或快照输入，验证稳定字典序及空集合的序列化形状。 [任务卡](../../tasks/cards/done/2011-stable-report-component-order.md)

### P3

- **2012 说明 LastStatus 的时间语义**（源码确认，检查号 6,16,31,42）
  - 证据与影响。recordComponentCheck 按 Tracker 接收结果更新 LastStatus。Manager 的发布版本可丢弃较旧回调，故两处状态可合法分离。公开字段缺少顺序说明。 [x/observability/ops/healthhttp/tracker.go:71](../../x/observability/ops/healthhttp/tracker.go#L71)
  - 最小方案。在字段或跟踪入口的 GoDoc 说明内部 Manager 的最近计量尝试与发布状态的关系，保留外部管理器快照回退。
  - 需要补齐的回归。用通道固定旧成功晚于新失败的顺序，断言 LastStatus、历史及已发布状态分别符合契约。 [任务卡](../../tasks/cards/done/2012-document-last-observed-component-attempt.md)
- **2013 应用 URL 入口补齐参数约束说明**（源码确认，检查号 6,16,37,52）
  - 证据与影响。App.URL 转发全部参数给 Router.URL。现有注释只列不存在、缺参和空参数，遗漏键必须成对及斜线约束。 [core/routing.go:42](../../core/routing.go#L42)
  - 最小方案。引用 Router.URL 的完整校验契约，保留 App 特有未配置错误的说明。
  - 需要补齐的回归。从 App.URL 核验单段值含斜线和未知键时返回空 URL 和错误。 [任务卡](../../tasks/cards/done/2013-app-url-document-router-validation.md)

## 基线执行顺序

先做 **2008**，从空 HEAD 的回环对照入手。接下来是 **2009** 文件轮换、**2010** 基础统计上界、**2011** 报告顺序，四张卡可分别交付。2020、2055、2058 各自依赖对应的行为修复。健康快照、关闭历史和重置边界先取证，按现有公开契约决定是否另开行为修改。 [56 张任务卡的优先级与依赖](../../tasks/cards/review-c3a77410/README.md)

| 实施风险 | 验收约束 |
|---|---|
| HEAD 自动长度调整影响调用方显式长度 | 用 2020 保留显式零，再验证小响应、已提交状态及控制器。 |
| 新轮换名字漏掉归档清理 | 用 2055 同时验证旧名字、活动文件和新候选名称。 |
| 饱和修正误伤独立累计 | 用 2058 验证 HTTP 错误数、总数与名称各自的上界及清空。 |

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
| 01 | 边界核验 | 九个稳定根分别读取职责与禁止项。报告组件排序及尝试计量归健康宿主，根健康模型只负责值与快照。 | [module.yaml:21](../../core/module.yaml#L21) |
| 02 | 边界核验 | boundary-check 通过。稳定包依赖按模块与内部 helper 的调用方规则校验。 | [dependency-rules.yaml:10](../../specs/dependency-rules.yaml#L10) |
| 03 | 边界核验 | 路由参数与配置仍由门面转发到 owning package。API 快照与边界校验通过，2060 待验证默认配置独立性。 | [plumego.go:60](../../plumego.go#L60) |
| 04 | 边界核验 | 主模块无第三方依赖项，stdlib 与内部 helper 的分类由边界门禁核验。 | [go.mod:3](../../go.mod#L3) |
| 05 | 源码取证 | 标准服务 New 显式装配路由与依赖。core 构造依赖入口有对应测试；装配重叠由 2015 继续补证。 | [app.go:33](../../reference/standard-service/internal/app/app.go#L33) |
| 06 | 已复现 | 与原生 HEAD 对照可见自动长度头差异，健康报告顺序及统计非负约束分别见 2011、2010。 | [dispatch.go:91](../../router/dispatch.go#L91) |
| 07 | 边界核验 | 严格清单门禁通过。此次未迁移或新增包装符号，逐个外部调用量未采集。 | [deprecation-inventory.yaml:7](../../specs/deprecation-inventory.yaml#L7) |
| 08 | 计划核验 | 56 卡各有主模块、改动范围、验收、验证命令及依赖，相关回归可分卡执行。 | [README.md:3](../../tasks/cards/review-c3a77410/README.md#L3) |
| 09 | 源码取证 | LastStatus 缺少观测与发布次序的说明。字段保留，2012 约定注释和受控验证。 | [tracker.go:71](../../x/observability/ops/healthhttp/tracker.go#L71) |
| 10 | 源码取证 | 先验证配置和加载 TLS，再准备 handler 和发布 server。2015 覆盖装配期间的状态读取。 | [http_handler.go:73](../../core/http_handler.go#L73) |
| 11 | 源码取证 | 快照序列化、替换和目录同步分相位；2046 补两种清理失败的观察证据。 | [persistence.go:214](../../store/kv/persistence.go#L214) |
| 12 | 源码取证 | 现有聚合 helper 提供饱和行为。2010 把相同边界应用于 Base，保持单条计数路径。 | [multi.go:112](../../metrics/multi.go#L112) |
| 13 | 源码取证 | 传输消费窄 ObserveHTTP，聚合侧保留完整接口。现行接口签名由 API 门禁对照。 | [collector.go:89](../../metrics/collector.go#L89) |
| 14 | 待补证 | 零超时禁用，非正预算取默认。两个预算都命中时的处理和精确阈值由 2028 核验。 | [timeout.go:77](../../middleware/timeout/timeout.go#L77) |
| 15 | 源码取证 | 带配置的构造先验证并返回错误。既有默认构造仍独立，未变更调用契约。 | [memory.go:49](../../store/cache/memory.go#L49) |
| 16 | 源码确认 | App.URL 对反查校验说明不全。另见 2012 的时间语义以及 2013 的 GoDoc 修订计划。 | [routing.go:42](../../core/routing.go#L42) |
| 17 | 已复现 | 签名兼容，回环 HEAD 发现长度头差异。HTTP/2 与真实代理的同路径未单独核验。 | [dispatch.go:159](../../router/dispatch.go#L159) |
| 18 | 源码取证 | 通过 Header、上下文和显式响应头传递请求 ID，2032 记录禁用注入时的隔离。 | [helpers.go:23](../../middleware/requestid/helpers.go#L23) |
| 19 | 源码取证 | 标准服务集中装配方法、路径与处理器并反馈登记错误，2018 和 2019 补登记快照与匹配行为。 | [routes.go:16](../../reference/standard-service/internal/app/routes.go#L16) |
| 20 | 待补证 | 源路径明确区分直达和预检，2033 补已有 Vary 多值及 next 次数。 | [cors.go:151](../../middleware/cors/cors.go#L151) |
| 21 | 源码取证 | 成功和失败由稳定入口写 JSON。编码详情错误触发降级，2024 补降级后 writer 失败。 | [response.go:114](../../contract/response.go#L114) |
| 22 | 待补证 | SQL 保留错误分类的路径已定位，2049 补 scan 与 Close 同时失败，2026 补多分支选择。 | [sql.go:424](../../store/db/sql.go#L424) |
| 23 | 当前回归 | 上轮已覆盖透传后 panic 的收尾，本轮新增普通写错误时尾字段验证卡 2029。 | [debug_errors.go:303](../../middleware/debug/debug_errors.go#L303) |
| 24 | 回环与现有回归 | 回环对照覆盖 HEAD 空表示，已有控制器、已选状态及连接接管相关回归。组合与新长度边界见 2008、2020。 | [dispatch.go:116](../../router/dispatch.go#L116) |
| 25 | 源码取证 | 输出写失败用固定字串。恢复和追踪继续由既有负例覆盖，未用真实凭据或生产日志核验。 | [glog_writer.go:249](../../log/glog_writer.go#L249) |
| 26 | 待补证 | 上下文检查先于写入及持久阶段，2044 用内存和重开双观察覆盖调用者预取消。 | [kv.go:113](../../store/kv/kv.go#L113) |
| 27 | 源码取证 | Trace、请求元数据及 RequestID 使用私有键与成对访问。2027 补 Baggage 快照形状。 | [trace.go:50](../../contract/trace.go#L50) |
| 28 | 源码取证 | 核对阶段、pendingStop 与重复停止。2016、2017 分别补探测退出和应用 Shutdown 重试。 | [lifecycle_group.go:214](../../core/lifecycle_group.go#L214) |
| 29 | 待补证 | 文件生命周期从 Open 延续到 Close。2021 补 Open 成功后 Stat 失败的关闭责任。 | [static.go:203](../../router/static.go#L203) |
| 30 | 待补证 | 写入失败触发 Close 并合并错误。2046 与 2049 补持久化与 SQL 错误链。 | [persistence.go:270](../../store/kv/persistence.go#L270) |
| 31 | 已复现 | 固定时钟下写两条正常日志后活动文件未换，计量归零，2009 修正轮换切换点。 | [glog_rotation.go:143](../../log/glog_rotation.go#L143) |
| 32 | 已复现 | 三种相关计数在同一锁内更新，合成上界下同步回绕。2010 补非负与 Clear 验收。 | [collector.go:145](../../metrics/collector.go#L145) |
| 33 | 源码取证 | 装配回调在 App 锁外执行。日志 writer 的串行和非重入边界见 LoggerConfig，阻塞外部写入未压测。 | [http_handler.go:53](../../core/http_handler.go#L53) |
| 34 | 待补证 | 健康详情顶层 copy，值保留浅复制；2053 检验依赖切片改写和扩容。 | [core.go:45](../../health/core.go#L45) |
| 35 | 待补证 | 有缓冲与重放两个预算。日志轮换固定时钟路径已复现，超时和日志阈值覆盖由 2028、2009 补齐。 | [timeout.go:266](../../middleware/timeout/timeout.go#L266) |
| 36 | 源码与现有回归 | 已有启动、倒序清理、失败重试和就绪用例。应用未启动先关闭、重复排空及并行装配见 2014–2017。 | [lifecycle_group.go:178](../../core/lifecycle_group.go#L178) |
| 37 | 回环与现有回归 | 检查显式方法、Any、HEAD、405 和缓存分支，2019 补显式死端与完整 Any 的冷热组合。 | [dispatch.go:178](../../router/dispatch.go#L178) |
| 38 | 源码与现有回归 | 检查编码先于提交、控制状态范围与 bodyless 规则。2024–2027 补失败链、请求形状及 copy 契约。 | [response.go:56](../../contract/response.go#L56) |
| 39 | 源码与现有回归 | 组合顺序保持显式。确认尾字段、Flush 与请求中止路径各自的所有权，2028–2037 补预算、能力和观察结果。 | [middleware.go:87](../../middleware/middleware.go#L87) |
| 40 | 源码与现有回归 | 核查现有认证拒绝测试与 timing-safe 调用。2038–2043 补配置、数据所有权和普通边界；部署策略未核验。 | [token.go:72](../../security/authn/token.go#L72) |
| 41 | 源码与现有回归 | 核查临时写入替换与重开测试，2044–2051 对取消、数据副本、边界和关闭错误单独验收。 | [persistence.go:245](../../store/kv/persistence.go#L245) |
| 42 | 源码与现有回归 | 版本、注册身份与关闭发布保持同锁。报告顺序已复现，整体报告和重置观测边界见 2061–2063。 | [manager.go:298](../../x/observability/ops/healthhttp/manager.go#L298) |
| 43 | 已复现 | 固定时钟下实测归档切换问题。既有文本控制字符、JSON 和循环深度回归通过，2055–2057 补命名和 writer 所有权。 | [glog_rotation.go:115](../../log/glog_rotation.go#L115) |
| 44 | 已复现 | 已核对顺序聚合、标签复制和独立子快照。基础统计越上界产生负值，独立错误分类和子清空失败见 2058、2059。 | [multi.go:82](../../metrics/multi.go#L82) |
| 45 | 现有回归与探针 | 既有门禁通过，临时探针观察当前行为。四项行为修复卡均要求常规入口回归。 | [head_regression_test.go:329](../../router/head_regression_test.go#L329) |
| 46 | 待补证 | 现有 SQL 失败回归通过，2049 补中间行扫描失败叠加关闭失败，其他负例见各卡。 | [sql.go:385](../../store/db/sql.go#L385) |
| 47 | 现有回归与探针 | 同名轮换用 synctest 固定时钟。新生命周期、队列和健康并发卡用通道或确定性时钟控制。 | [lifecycle_group_test.go:585](../../core/lifecycle_group_test.go#L585) |
| 48 | 范围说明 | 定位公开入口基准。审查给出行为与容量验收，未提出吞吐或内存改善主张，未重跑基准。 | [router_bench_test.go:37](../../router/router_bench_test.go#L37) |
| 49 | 门禁核验 | 格式检查通过，独立 lint 为 0 项。当前审查增量是 Markdown，无 Go 格式化写入。 | [Makefile:19](../../Makefile#L19) |
| 50 | 门禁核验 | 以新增报告和卡片选择 docs_only，审查基线另执行稳定包 race、vet、边界和 API 核验。 | [Makefile:22](../../Makefile#L22) |
| 51 | 门禁与范围 | 稳定模块语句总覆盖率 96.8%。真实时钟同名频率、生产计数容量触达和部署行为未测。 | [Makefile:78](../../Makefile#L78) |
| 52 | 交付核验 | 上轮交付已提交。本轮报告记录命令与限制，未来行为或文档落地按卡同步，当前公开 API 无变更。 | [stable-quality-review-999d5e4d.md:153](stable-quality-review-999d5e4d.md#L153) |

## 本机复现

| 临时探针 | 观察结果 |
|---|---|
| `TestReviewEmptyHeadLength` | 同一个 202 处理器不写 body，标准库直接挂载的 Content-Length 缺失，Router 挂载得到 0。二者正文均为空。 |
| `TestReviewEqualClockRotationAfterTwoWrites` | 同一固定时刻向私有文件 logger 写两条日志，活动文件路径未变，磁盘 2,097,262 字节，内存计量 0。 |
| `TestReviewBaseCollectorCounterBoundary` | 同包测试预置计数上界后经 Record 记录一条失败，直接和聚合的三个累计量均为 -9223372036854775808。 |
| `TestReviewReportListOrder` | 内置 Manager 用六个普通组件，连续两次报告可见不同排列，组件集合相同。 |

临时探针源码在工作区外。同包日志与指标探针通过 Go overlay 加载，测试断言观察到的基线偏差。固定时钟碰撞频率和生产计数触达上界均未测。

## 基线验证记录

| 命令 | 结果 |
|---|---|
| `git show --format= --check HEAD` | 通过，基线 c3a77410。 |
| `go test -race -count=1 -timeout 60s ./core ./router ./contract ./middleware/... ./security/... ./store/... ./health ./log ./metrics ./x/observability/ops/healthhttp .` | 通过，禁用结果缓存重跑。 |
| 对同一包集合运行 `go vet` | 通过。 |
| `make boundary-check api-check fmt-check lint stable-coverage` | 通过，稳定 API 快照匹配，lint 0 项，稳定语句覆盖率 96.8%。 |
| `go test -race -v -count=1 /tmp/c3a-http_test.go` | 两项通过，确认 HEAD 和组件顺序。 |
| `go test -race -v -overlay=/tmp/c3a-overlay.json ./log ./metrics -run ^TestReview` | 两项通过，确认文件计量和统计回绕。 |
| `make validate-diff-dry` 和 `make validate-diff` | 通过，选择 docs_only。该配置没有额外运行命令。 |
| 逐卡 `make check-card` 及本地交付检查 | 56 张卡通过，52 个检查号与 352 条本地链接和行号通过校验。工作区仅新增 58 个 Markdown 文件。 |

基线审查未重跑参考模块、CLI、网站构建及单独基准，也未验证真实依赖、生产配置或原生跨平台。审查阶段仅新增报告与卡片。

## 实施结果

2008–2063 已完成。四个行为回归在原实现上失败，修复后的定向竞态套件通过。补证复用了能够覆盖相同触发与断言的现有用例，并在缺少断言时补齐。稳定公开签名和依赖保持原样。 [56 张实施卡](../../tasks/cards/review-c3a77410/README.md)

| 卡片 | 最终行为 |
|---|---|
| 2008、2020 | 未写正字节时 HEAD 保留缺失的自动长度，显式零与已有长度继续发送。小响应按实际写入推断。 |
| 2009、2055 | 轮换候选以独占模式创建。同名时以原时间戳格式生成后续候选，保留旧归档兼容。打开失败时当前 writer 和已有字节保持可用。 |
| 2010、2058 | Base 记录总数、错误数与每名称计数在一个锁内各自饱和。HTTP 分类及 Clear 语义保持。 |
| 2011、2012 | 报告组件名按字典序。LastStatus 说明最近计量结果可晚于管理器更新，外部实现沿用快照结果。 |
| 2013 | App.URL 的 GoDoc 引用 Router.URL 参数约束，应用入口回归覆盖斜线和未知键。 |

2059、2061–2063 是分析卡。实测聚合 Clear 中途异常时第一项已重置，后两项保留，消除故障后可重试。健康报告读取可以跨管理器快照。关闭后批检查仍记录一次调用历史，组件回调与计数不会增长。Reset 前启动的检查在 Reset 后完成时计入新统计。四项均记录当前行为，没有更改其实现。

### 实施门禁

| 命令 | 结果 |
|---|---|
| 四项行为回归在 `c3a77410` 的原实现上运行 | 四项均按预期失败。修复后的同一组回归通过。 |
| 按各卡执行模块竞态测试，并对四项修复及关联边界执行 `go test -race -count=10` | 定向通过，八个相关用例重复十次通过。最后补充的响应错误双分类回归由 `go test -race -timeout 60s ./contract` 再次验证。 |
| `go test -race -count=1 -v -overlay=/tmp/c3a-impl-analysis-overlay.json ./metrics ./x/observability/ops/healthhttp -run ^TestReview` | 四个分析探针通过。 |
| `make validate-diff-dry`、`make validate-diff` | 选择 cross_module_public_api_release，执行完整 `make gates` 并通过。 |
| `make fmt-check lint`、`make test`、`make stable-coverage` | 通过。lint 0 项，稳定模块语句覆盖率 96.8%。 |
| `make website-sync`、`make website-gates` | 通过，生成数据无差异。主题样式中既有的 `:global` CSS 压缩警告仍可见。 |
| 逐卡 `make check-card` 和 `git diff --check` | 通过，56 张归档卡、52 个检查号和 417 条本地链接及源码行号均已校验。 |

固定时钟同名频率、极限计数的生产触达频率与原生跨平台仍未测量。
