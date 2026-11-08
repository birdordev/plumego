import { MODULE_FACTS } from '../generated/modules';
import { RELEASE_FACTS } from '../generated/releases';
import type { Locale } from '../lib/locales';

export type { Locale };

export const SITE = {
  name: 'Plumego',
  githubUrl: 'https://github.com/birdordev/plumego',
  repoPath: 'reference/standard-service',
  currentVersion: RELEASE_FACTS.currentVersion,
};

export const SITE_COPY: Record<Locale, { footerTagline: string }> = {
  en: {
    footerTagline: 'stdlib-only Go HTTP toolkit — explicit by design, machine-readable specs by structure.',
  },
  zh: {
    footerTagline: 'stdlib only 的 Go HTTP 工具包——显式设计，机器可读规范内置于结构。',
  },
};

export const NAV_LINKS: Record<Locale, Array<{ label: string; href: string }>> = {
  en: [
    { label: 'Why', href: '/why-plumego' },
    { label: 'Docs', href: '/docs' },
    { label: 'Extensions', href: '/extensions' },
    { label: 'Compare', href: '/compare' },
    { label: 'Examples', href: '/examples' },
    { label: 'Releases', href: '/releases' },
  ],
  zh: [
    { label: '为何选择', href: '/zh/why-plumego' },
    { label: '文档', href: '/zh/docs' },
    { label: '扩展', href: '/zh/extensions' },
    { label: '框架对比', href: '/zh/compare' },
    { label: '示例', href: '/zh/examples' },
    { label: '发布', href: '/zh/releases' },
  ],
};

export const FOOTER_GROUPS: Record<Locale, Array<{ title: string; links: Array<{ label: string; href: string }> }>> = {
  en: [
    {
      title: 'Product',
      links: [
        { label: 'Docs', href: '/docs' },
        { label: 'Why Plumego', href: '/why-plumego' },
        { label: 'Use Cases', href: '/use-cases' },
        { label: 'Examples', href: '/examples' },
        { label: 'Architecture', href: '/architecture' },
        { label: 'Compare & Benchmarks', href: '/compare' },
        { label: 'Extensions (x/*)', href: '/extensions' },
        { label: 'Agent Workflow', href: '/agent-workflow' },
        { label: 'Migrate', href: '/migrate' },
      ],
    },
    {
      title: 'Start',
      links: [
        { label: 'Getting Started', href: '/docs/getting-started' },
        { label: 'Reference App', href: '/docs/reference-app' },
        { label: 'Migrate from Gin', href: '/docs/guides/migrate-from-gin-echo' },
        { label: 'Migrate from Echo', href: '/docs/guides/migrate-from-gin-echo' },
        { label: 'Migrate from Chi', href: '/docs/guides/migrate-from-chi' },
        { label: 'Migration Hub', href: '/migrate' },
        { label: 'FAQ', href: '/docs/faq' },
      ],
    },
    {
      title: 'Project',
      links: [
        { label: 'Releases', href: '/releases' },
        { label: 'Stability', href: '/stability' },
        { label: 'Roadmap', href: '/roadmap' },
        { label: 'Status', href: '/status' },
        { label: 'Changelog', href: `${SITE.githubUrl}/releases` },
        { label: 'GitHub', href: SITE.githubUrl },
        { label: 'Issues', href: `${SITE.githubUrl}/issues` },
        { label: 'Discussions', href: `${SITE.githubUrl}/discussions` },
      ],
    },
  ],
  zh: [
    {
      title: '产品',
      links: [
        { label: '文档', href: '/zh/docs' },
        { label: '为什么选择 Plumego', href: '/zh/why-plumego' },
        { label: '使用场景', href: '/zh/use-cases' },
        { label: '示例', href: '/zh/examples' },
        { label: '架构', href: '/zh/architecture' },
        { label: '框架对比与性能', href: '/zh/compare' },
        { label: '扩展 (x/*)', href: '/zh/extensions' },
        { label: 'Agent 工作流', href: '/zh/agent-workflow' },
        { label: '迁移指南', href: '/zh/migrate' },
      ],
    },
    {
      title: '起步',
      links: [
        { label: '开始使用', href: '/zh/docs/getting-started' },
        { label: '参考应用', href: '/zh/docs/reference-app' },
        { label: '从 Gin 迁移', href: '/zh/docs/guides/migrate-from-gin-echo' },
        { label: '从 Echo 迁移', href: '/zh/docs/guides/migrate-from-gin-echo' },
        { label: '从 Chi 迁移', href: '/zh/docs/guides/migrate-from-chi' },
        { label: '迁移中心', href: '/zh/migrate' },
        { label: '常见问题', href: '/zh/docs/faq' },
      ],
    },
    {
      title: '项目',
      links: [
        { label: '发布', href: '/zh/releases' },
        { label: '稳定性', href: '/zh/stability' },
        { label: '路线图', href: '/zh/roadmap' },
        { label: '状态总览', href: '/zh/status' },
        { label: 'Changelog', href: `${SITE.githubUrl}/releases` },
        { label: 'GitHub', href: SITE.githubUrl },
        { label: 'Issues', href: `${SITE.githubUrl}/issues` },
        { label: 'Discussions', href: `${SITE.githubUrl}/discussions` },
      ],
    },
  ],
};

export const HOME_COPY = {
  en: {
    eyebrow: `stdlib-only · zero external deps · ${RELEASE_FACTS.currentVersion}`,
    headline: 'Go HTTP your agent and your team can both read.',
    summary:
      'Route registration, middleware order, and dependency wiring stay in one explicit file — readable in every code review. Machine-readable specs give AI coding agents the same operating model as your senior reviewers. <code>net/http</code> compatible: existing handlers work without changes.',
    primaryCta: { label: 'Get Started', href: '/docs/getting-started' },
    secondaryCta: { label: 'Compare frameworks', href: '/compare' },
    scenarioCards: [
      {
        kicker: 'REST API',
        title: 'Build a REST API in minutes',
        code: `r := router.New()
api := r.Group("/api/v1")
api.Get("/users/:id", users.Get)
api.Post("/users",    users.Create)`,
        body: 'Trie-based routing with path params, middleware groups, and typed responses — all using standard net/http handlers. No custom context types.',
        href: '/docs/guides/build-rest-resource',
        label: 'REST API guide',
        maturity: null,
      },
      {
        kicker: 'WebSocket',
        title: 'Add real-time without touching core',
        code: `ws, err := websocket.New(websocket.DefaultWebSocketConfig())
if err != nil {
    return err
}
if err := ws.RegisterRoutes(r); err != nil {
    return err
}

// broadcast to all connected clients
ws.Hub().BroadcastAll(websocket.OpcodeText, []byte("update"))`,
        body: 'x/websocket adds a managed hub on top of the stable HTTP kernel. Routes stay explicit; transport concerns stay separate.',
        href: '/docs/modules/x/websocket',
        label: 'WebSocket module',
        maturity: 'beta',
      },
      {
        kicker: 'Multi-tenant SaaS',
        title: 'Isolate tenants without changing core',
        code: `api.Use(resolve.Middleware(resolve.Options{
    HeaderName: "X-Tenant-ID",
}))
api.Get("/data", data.List)

// tenant identity resolved at transport
// stable roots never see tenant logic`,
        body: 'x/tenant attaches per-tenant identity and policy at the transport layer. The stable root API stays unchanged as your tenant logic evolves.',
        href: '/docs/modules/x/tenant',
        label: 'Tenant module',
        maturity: 'beta',
      },
    ],
    stabilityBandTitle: 'Know exactly what you can rely on.',
    stabilityBandItems: [
      { label: '9 stable modules', detail: 'core · router · contract · middleware · security · store · health · log · metrics', status: 'stable', href: '/stability' },
      { label: '7 beta extensions', detail: 'API frozen between refs', status: 'beta', href: '/stability' },
      { label: '7 experimental families', detail: 'Evaluate before adopting', status: 'experimental', href: '/stability' },
    ],
    stabilityBandCta: { label: 'Project status & release evidence', href: '/status' },
    moduleTitle: 'Stable modules.',
    moduleLead: '9 packages with the v1 stable-root compatibility promise. The recommended starting scope for production evaluation.',
    moduleBaseImport: 'github.com/birdordev/plumego',
    modules: [
      { name: 'core',       description: 'App bootstrap, server lifecycle, and configuration assembly',                        docHref: '/docs/modules/core' },
      { name: 'router',     description: 'http.Handler-compatible mux with groups and path parameter matching',                docHref: '/docs/modules/router' },
      { name: 'contract',   description: 'Typed request/response envelope: WriteResponse, WriteError, error builders',         docHref: '/docs/modules/contract' },
      { name: 'middleware', description: 'Transport-only middleware: request ID, logger, recovery, CORS, rate limiter',        docHref: '/docs/modules/middleware' },
      { name: 'security',   description: 'Auth identity primitives, JWT validation, security headers, input sanitization, and abuse guards', docHref: '/docs/modules/security' },
      { name: 'store',      description: 'Base persistence abstractions: file, KV, cache, idempotency, and DB helper primitives',          docHref: '/docs/modules/store' },
      { name: 'health',     description: 'Health status types, readiness helpers, and component health models — types only, not HTTP',      docHref: '/docs/modules/health' },
      { name: 'log',        description: 'StructuredLogger interface, NewLogger base construction, and structured field helpers',           docHref: '/docs/modules/log' },
      { name: 'metrics',    description: 'Recorder and AggregateCollector interfaces, HTTPObserver, and collector composition',             docHref: '/docs/modules/metrics' },
    ],
    extensionTitle: 'Extension families (x/*).',
    extensionNote: '7 beta — API frozen between release refs: x/rest, x/gateway, x/websocket, x/observability, x/tenant, x/frontend, x/messaging. 7 experimental primary families for product-specific capability work.',
    extensionDocsHref: '/docs/x-family',
    extensionReleasesHref: '/releases',
    adoptionTitle: 'Where to go next.',
    adoptionBody:
      'Evaluate fit before the technical details. Browse the modules overview to find the right x/* family. See how Plumego compares to Gin, Echo, and Chi.',
    adoptionCards: [
      {
        kicker: 'fit',
        title: 'Why Plumego',
        body: 'Start here when the question is whether Plumego fits your team, your service shape, and your review expectations — before investing time in the technical details.',
        href: '/why-plumego',
        label: 'Evaluate fit',
      },
      {
        kicker: 'agent-first',
        title: 'Agent Workflow',
        body: 'See how machine-readable specs route AI coding agents to the right module, enforce boundaries in CI, and share the same operating model as human reviewers.',
        href: '/agent-workflow',
        label: 'See agent model',
      },
      {
        kicker: 'extensions',
        title: 'x/* Extension Families',
        body: 'Browse all x/* capability families by maturity tier. 7 beta families with frozen APIs, plus experimental families for product-specific capability work.',
        href: '/extensions',
        label: 'Browse extensions',
      },
      {
        kicker: 'comparison',
        title: 'Compare Frameworks',
        body: 'Gin, Echo, Fiber, Chi, and Plumego — handler signatures, response contracts, stdlib compatibility, migration cost, and honest benchmarks side by side.',
        href: '/compare',
        label: 'See comparison',
      },
    ],
    finalTitle: 'Start from the reference app. Pick the right x/* family. Keep boundaries clear.',
    finalBody:
      'Clone reference/standard-service for the stable kernel path. Browse the modules overview to find the right capability family. Let the control plane guide coding assistants and reviewers alike.',
    finalPrimary: { label: 'Read Docs', href: '/docs' },
    finalSecondary: { label: 'Browse Extensions', href: '/extensions' },
    contrastTitle: 'The difference shows in code review.',
    contrastLead:
      'When routes are spread across packages, a reviewer has to open each one to understand what paths exist and what middleware runs. Plumego keeps the full route map in one explicit file — and adds a structured <code>contract</code> layer so error and success responses stay consistent across all handlers.',
    contrastBeforeLabel: 'routes split across packages',
    contrastAfterLabel: 'plumego: one file, one contract',
  },
  zh: {
    eyebrow: `stdlib only · 零外部依赖 · ${RELEASE_FACTS.currentVersion}`,
    headline: 'AI agent 和你的团队都能读懂的 Go HTTP 框架。',
    summary:
      '路由注册、中间件顺序和依赖装配全在一个显式文件里——每次代码评审都能直接看到。机器可读规范给 AI 编程助手和资深评审者提供同一套操作模型。兼容 <code>net/http</code>：现有 handler 无需修改即可接入。',
    primaryCta: { label: '开始使用', href: '/zh/docs/getting-started' },
    secondaryCta: { label: '框架横向对比', href: '/zh/compare' },
    scenarioCards: [
      {
        kicker: 'REST API',
        title: '几分钟搭一个 REST API',
        code: `r := router.New()
api := r.Group("/api/v1")
api.Get("/users/:id", users.Get)
api.Post("/users",    users.Create)`,
        body: '基于 trie 树的路由，支持路径参数、中间件组和类型化响应，全部使用标准 net/http handler，无自定义 context 类型。',
        href: '/zh/docs/guides/build-rest-resource',
        label: 'REST API 指南',
        maturity: null,
      },
      {
        kicker: 'WebSocket',
        title: '不动内核，加入实时能力',
        code: `ws, err := websocket.New(websocket.DefaultWebSocketConfig())
if err != nil {
    return err
}
if err := ws.RegisterRoutes(r); err != nil {
    return err
}

// 向所有连接的客户端广播
ws.Hub().BroadcastAll(websocket.OpcodeText, []byte("update"))`,
        body: 'x/websocket 在稳定 HTTP 内核上加了一个托管 hub，路由保持显式，传输层关注点保持独立。',
        href: '/zh/docs/modules/x/websocket',
        label: 'WebSocket 模块',
        maturity: 'beta',
      },
      {
        kicker: '多租户 SaaS',
        title: '不改内核，隔离多租户',
        code: `api.Use(resolve.Middleware(resolve.Options{
    HeaderName: "X-Tenant-ID",
}))
api.Get("/data", data.List)

// 租户身份在传输层解析
// 稳定根永远感知不到租户逻辑`,
        body: 'x/tenant 在传输层附加每租户身份与策略，稳定根 API 不受租户逻辑演进影响。',
        href: '/zh/docs/modules/x/tenant',
        label: '租户模块',
        maturity: 'beta',
      },
    ],
    stabilityBandTitle: '清楚知道哪些可以依赖。',
    stabilityBandItems: [
      { label: '9 个稳定模块', detail: 'core · router · contract · middleware · security · store · health · log · metrics', status: 'stable', href: '/zh/stability' },
      { label: '7 个 beta 扩展', detail: 'ref 间 API 冻结', status: 'beta', href: '/zh/stability' },
      { label: '7 个实验性家族', detail: '采用前请先评估', status: 'experimental', href: '/zh/stability' },
    ],
    stabilityBandCta: { label: '项目状态与发布证据', href: '/zh/status' },
    moduleTitle: '稳定模块。',
    moduleLead: '9 个包承载 v1 稳定根兼容性承诺，是推荐的生产评估起点。',
    moduleBaseImport: 'github.com/birdordev/plumego',
    modules: [
      { name: 'core',       description: '应用启动、服务器生命周期与配置组装',                              docHref: '/zh/docs/modules/core' },
      { name: 'router',     description: '兼容 http.Handler 的多路复用器，支持路由组和路径参数匹配',         docHref: '/zh/docs/modules/router' },
      { name: 'contract',   description: '请求/响应类型封装：WriteResponse、WriteError、错误构建器',        docHref: '/zh/docs/modules/contract' },
      { name: 'middleware', description: '传输层中间件：请求 ID、日志、recover、CORS、限流',               docHref: '/zh/docs/modules/middleware' },
      { name: 'security',   description: 'auth identity 基础元语、JWT 验证、security headers、输入安全与防滥用保护原语',  docHref: '/zh/docs/modules/security' },
      { name: 'store',      description: '基础持久化抽象：文件、KV、缓存、幂等性与 DB 辅助 primitive',                  docHref: '/zh/docs/modules/store' },
      { name: 'health',     description: 'health status 类型、readiness 助手与组件健康模型——仅提供类型，非 HTTP 包',     docHref: '/zh/docs/modules/health' },
      { name: 'log',        description: 'StructuredLogger 接口、NewLogger 基础构造与日志字段助手',                    docHref: '/zh/docs/modules/log' },
      { name: 'metrics',    description: 'Recorder 与 AggregateCollector 接口、HTTPObserver 及多 collector 组合',      docHref: '/zh/docs/modules/metrics' },
    ],
    extensionTitle: '扩展家族 (x/*)。',
    extensionNote: '7 个 beta（版本引用间 API 冻结）：x/rest、x/gateway、x/websocket、x/observability、x/tenant、x/frontend、x/messaging。另有 7 个实验性主入口家族，用于产品能力工作。',
    extensionDocsHref: '/zh/docs/x-family',
    extensionReleasesHref: '/zh/releases',
    adoptionTitle: '下一步去哪里。',
    adoptionBody:
      '先评估适用性，再深入技术细节。在模块总览中找到合适的 x/* 家族。看看 Plumego 与 Gin、Echo、Chi 的横向对比。',
    adoptionCards: [
      {
        kicker: 'fit',
        title: '为什么选择 Plumego',
        body: '当问题是 Plumego 是否适合你的团队、服务形态和评审预期——而不是技术细节时，从这里开始。',
        href: '/zh/why-plumego',
        label: '判断是否适合',
      },
      {
        kicker: 'agent-first',
        title: 'Agent 工作流',
        body: '了解机器可读规范如何在写代码前就把 AI 助手路由到正确模块，在 CI 中执行边界约束，与人工评审者共享同一套操作模型。',
        href: '/zh/agent-workflow',
        label: '查看 Agent 模型',
      },
      {
        kicker: 'extensions',
        title: 'x/* 扩展家族',
        body: '按成熟度层级浏览所有 x/* 能力家族。7 个 API 冻结的 beta 家族，加上用于产品能力工作的实验性家族。',
        href: '/zh/extensions',
        label: '浏览扩展家族',
      },
      {
        kicker: 'comparison',
        title: '框架横向对比',
        body: 'Gin、Echo、Fiber、Chi 与 Plumego 的 handler 签名、响应 contract、stdlib 兼容性、迁移成本与基准数据，诚实并列呈现。',
        href: '/zh/compare',
        label: '查看对比',
      },
    ],
    finalTitle: '从 reference app 起步。选对 x/* 家族。保持边界清晰。',
    finalBody: 'clone reference/standard-service 沿稳定内核路径出发。在模块总览中找到合适的能力家族。让控制平面同时引导编程助手和代码评审者。',
    finalPrimary: { label: '阅读文档', href: '/zh/docs' },
    finalSecondary: { label: '浏览扩展家族', href: '/zh/extensions' },
    contrastTitle: '差异在代码评审时最明显。',
    contrastLead:
      '当路由分散在各个包里时，评审者必须逐个打开才能知道有哪些路径和中间件在运行。Plumego 把完整路由表放在一个显式文件里——同时加入结构化的 <code>contract</code> 层，让所有 handler 的错误响应和成功响应保持一致。',
    contrastBeforeLabel: '路由分散在各包里',
    contrastAfterLabel: 'plumego：一个文件，一套 contract',
  },
} as const;

export const ROADMAP_COPY = {
  en: {
    title: 'Roadmap',
    description: 'What Plumego is hardening now, what comes next, and what stays out of scope.',
    eyebrow: 'Repository Direction',
    introTitle: 'How to read the roadmap.',
    introBody:
      'The roadmap is not a promise that every package moves at the same speed. It explains what is already treated as baseline, which areas are being hardened now, and which ambitions are still gated by policy, tests, and documentation.',
    guideCards: [
      {
        kicker: 'baseline',
        title: 'Read the baseline first',
        body: 'The baseline tells you what already defines the default Plumego path and should not be inferred from planned work.',
      },
      {
        kicker: 'focus',
        title: 'Use in-progress work as the real near-term signal',
        body: 'If an area is in progress, it is receiving active attention now. Planned work is directional, not a compatibility statement.',
      },
      {
        kicker: 'non-goals',
        title: 'Treat non-goals as architectural boundaries',
        body: 'The non-goals are as important as the roadmap itself because they prevent the repo from drifting back into hidden compatibility layers.',
      },
    ],
    timelineTitle: 'Current direction',
    timelineBody:
      'These sections are synchronized from the repository facts, then framed here so a reader can distinguish established baseline, active work, and deferred scope.',
    orderTitle: 'Suggested execution order',
    orderBody:
      'When multiple lines of work are open, this sequence shows the intended tightening order rather than a guarantee of exact release timing.',
    sections: [
      {
        title: 'Current baseline',
        items: [
          'stable roots with explicit boundaries',
          'canonical reference app under reference/standard-service',
          'repo-native control plane under docs/, specs/, and tasks/',
        ],
      },
      {
        title: 'In progress',
        items: [
          'x/ai stabilization and runnable examples',
          'docs and onboarding sync for v1 users',
          'deeper extension test coverage where compatibility is still experimental',
        ],
      },
      {
        title: 'Planned',
        items: [
          'x/tenant production-readiness hardening',
          'clearer x/data and x/fileapi operational guidance',
          'extension stability criteria before any promotion discussion',
        ],
      },
      {
        title: 'Non-goals',
        items: [
          'reintroducing hidden compatibility layers',
          'moving tenant or data-topology logic back into stable roots',
          'marking x/* as GA without policy, tests, and docs',
        ],
      },
    ],
  },
  zh: {
    title: '路线图',
    description: 'Plumego 当前在补什么、下一步做什么，以及明确不做什么。',
    eyebrow: 'Repository Direction',
    introTitle: '怎么阅读这份路线图。',
    introBody:
      '路线图不是在暗示所有包会以同样速度推进。它要说明的是：哪些已经属于默认基线，哪些正在补强，哪些仍然只是方向而不是兼容性承诺。',
    guideCards: [
      {
        kicker: 'baseline',
        title: '先看基线',
        body: '基线说明哪些东西已经构成默认 Plumego 路径，不应该从计划项里反向推断。',
      },
      {
        kicker: 'focus',
        title: '进行中才代表近期真实信号',
        body: '如果某个方向处于进行中，说明它正在被主动补强。计划中的内容是方向，不是兼容性保证。',
      },
      {
        kicker: 'non-goals',
        title: '把非目标当作架构边界',
        body: '非目标和路线图本身一样重要，因为它们约束仓库不要重新滑回隐藏兼容层。',
      },
    ],
    timelineTitle: '当前方向',
    timelineBody:
      '下面这些分组由仓库事实同步而来，这里再补一层解释，帮助读者分清哪些是已建立基线、哪些是当前工作、哪些仍是后续范围。',
    orderTitle: '建议执行顺序',
    orderBody:
      '当多条工作线同时存在时，这个顺序表达的是优先收敛方向，而不是精确发布时间承诺。',
    sections: [
      {
        title: '当前基线',
        items: [
          '稳定根已经建立清晰边界',
          'reference/standard-service 作为 canonical 参考应用',
          'docs/、specs/、tasks/ 已形成仓库控制面',
        ],
      },
      {
        title: '进行中',
        items: [
          'x/ai 的稳定化与可运行示例',
          'v1 用户视角的文档与 onboarding 同步',
          '仍属 experimental 的扩展模块补强测试深度',
        ],
      },
      {
        title: '计划中',
        items: [
          'x/tenant 的生产可用性加强',
          'x/data 与 x/fileapi 的运维指引补齐',
          '在扩展晋级前先定义统一稳定性标准',
        ],
      },
      {
        title: '明确不做',
        items: [
          '重新引入隐藏兼容层',
          '把租户或数据拓扑逻辑塞回稳定根',
          '在缺少策略、测试和文档时把 x/* 标成 GA',
        ],
      },
    ],
  },
} as const;

export const ARCHITECTURE_COPY = {
  en: {
    title: 'Architecture',
    description: 'Stable roots, extension families, and the canonical request path that define the Plumego mental model.',
    eyebrow: 'System Topology',
    introTitle: 'Read the architecture as a boundary map.',
    introBody:
      'Plumego is easiest to evaluate when you can see which responsibilities stay in the kernel, which ones branch outward into extensions, and where the default request path begins and ends.',
    guideCards: [
      {
        kicker: 'stable roots',
        title: 'Keep long-lived responsibilities narrow',
        body: 'Routing, contracts, transport middleware, and storage-facing primitives should stay legible enough to defend compatibility over time.',
      },
      {
        kicker: 'extension rule',
        title: 'Push optional capability work outward',
        body: 'Product or protocol-specific expansion should begin in x/* families so the kernel does not absorb every fast-moving concern.',
      },
      {
        kicker: 'canonical path',
        title: 'Teach one request path first',
        body: 'The website, docs, and reference app all point toward one readable route from bootstrap to write path before asking readers to branch into deeper packages.',
      },
    ],
    sectionTitle: 'Three layers you should be able to classify immediately',
    sectionBody:
      'If a new reader cannot tell whether a change belongs to stable roots, a capability family, or the canonical app path, the site is not doing enough explanatory work.',
    layers: [
      {
        kicker: 'kernel',
        title: 'Stable roots',
        body: 'Core modules carry the strongest compatibility burden. They should stay narrow, boring, and easy to reason about in code review.',
        items: MODULE_FACTS.stableRoots,
      },
      {
        kicker: 'extensions',
        title: 'Primary capability families',
        body: 'These families are useful, but they do not pretend to share the same stability profile as the smallest kernel surface.',
        items: MODULE_FACTS.primaryExtensionFamilies,
      },
      {
        kicker: 'workflow',
        title: 'Canonical request path',
        body: 'The default app path gives teams one bootstrap model, one routing flow, and one shared place to begin before extension work starts.',
        items: ['docs/getting-started', 'docs/reference-app', 'internal/app/app.go', 'internal/app/routes.go'],
      },
    ],
    flowTitle: 'How to inspect one request without guessing',
    flowBody:
      'The quickest architecture read starts at the reference app, confirms the app-local wiring, and only then expands outward into packages that obviously belong to the feature or protocol being added.',
    flowSteps: [
      {
        label: '01',
        title: 'Start in the reference app',
        body: 'Read the default service shape first so bootstrap and route ownership stay visible.',
      },
      {
        label: '02',
        title: 'Confirm the app-local constructor',
        body: 'Check where dependencies are assembled before treating helpers or packages as architectural entry points.',
      },
      {
        label: '03',
        title: 'Trace routes before handlers',
        body: 'Route registration should tell you which request path is public, what middleware runs, and where transport control ends.',
      },
    ],
  },
  zh: {
    title: '架构',
    description: '帮助读者看清 Plumego 的稳定根、扩展家族与 canonical 请求路径如何构成统一心智模型。',
    eyebrow: 'System Topology',
    introTitle: '把架构页当作边界地图来读。',
    introBody:
      '判断 Plumego 最容易的方式，是先看清哪些职责留在内核、哪些能力向 x/* 扩展家族外分，以及默认请求路径从哪里开始、在什么地方结束。',
    guideCards: [
      {
        kicker: 'stable roots',
        title: '让长期职责保持收敛',
        body: '路由、契约、传输中间件以及面向存储的基础原语，应当保持足够清晰，才能长期守住兼容性。',
      },
      {
        kicker: 'extension rule',
        title: '把可选能力向外扩展',
        body: '产品能力或协议适配应该从 x/* 家族开始，而不是不断吸入内核，最终让默认路径失去边界。',
      },
      {
        kicker: 'canonical path',
        title: '先教会读者一条默认请求路径',
        body: '网站、文档和 reference app 都应该先把从 bootstrap 到 write path 的默认路径讲清楚，再让读者进入更深的扩展区域。',
      },
    ],
    sectionTitle: '读者应该能立刻分清的三层结构',
    sectionBody:
      '如果一个新读者分不清某个改动属于稳定根、能力家族还是 canonical 应用路径，说明站点还没有把架构解释到位。',
    layers: [
      {
        kicker: 'kernel',
        title: '稳定根',
        body: '核心模块承担最强的兼容性负担，因此应保持收敛、克制，并且在代码评审时容易理解。',
        items: MODULE_FACTS.stableRoots,
      },
      {
        kicker: 'extensions',
        title: '主要能力家族',
        body: '这些扩展很重要，但不应该假装与最小内核表面享受同一等级的稳定性承诺。',
        items: MODULE_FACTS.primaryExtensionFamilies,
      },
      {
        kicker: 'workflow',
        title: 'canonical 请求路径',
        body: '默认应用路径给团队提供统一 bootstrap 模型、统一路由流向，以及开始理解仓库的共同入口。',
        items: ['docs/getting-started', 'docs/reference-app', 'internal/app/app.go', 'internal/app/routes.go'],
      },
    ],
    flowTitle: '不靠猜测地检查一条请求路径',
    flowBody:
      '最快的架构阅读方式，是先从 reference app 入手，确认 app-local wiring，然后再向外展开到那些显然属于具体功能或协议的包。',
    flowSteps: [
      {
        label: '01',
        title: '先从 reference app 开始',
        body: '先看默认服务形态，把 bootstrap 和 route ownership 读清楚。',
      },
      {
        label: '02',
        title: '确认应用本地构造器',
        body: '先检查依赖是如何被组装的，再判断辅助包是不是架构入口。',
      },
      {
        label: '03',
        title: '先追 routes，再进 handlers',
        body: 'route 注册应该先告诉你公开请求路径、middleware 顺序以及 transport 控制在哪里停止。',
      },
    ],
  },
} as const;

export const RELEASE_COPY = {
  en: {
    title: 'Releases',
    description: 'Release posture, compatibility expectations, and the current support matrix.',
    eyebrow: 'Release Posture',
    introTitle: 'How to read release posture.',
    introBody:
      'This page exists to make compatibility boundaries explicit. It should help you distinguish what is safe to adopt through the canonical path, what is still evolving, and where release ambition is intentionally held back by policy or test depth.',
    guideCards: [
      {
        kicker: 'canonical',
        title: 'Start from the canonical path',
        body: 'If you are evaluating Plumego for real use, begin with the reference app and the documented default path before reading optional surfaces as if they were all equally mature.',
      },
      {
        kicker: 'matrix',
        title: 'Read the support matrix as a boundary map',
        body: 'The matrix is not marketing copy. It tells you which areas carry stronger compatibility expectations and which still require caution.',
      },
      {
        kicker: 'roadmap',
        title: 'Separate release posture from roadmap ambition',
        body: 'A package can be interesting and actively developed without yet carrying the same stability promise as the stable roots.',
      },
    ],
    principlesTitle: 'Compatibility principles',
    principlesBody:
      'Release posture is framed by a few simple rules: the canonical path comes first, stable roots stay narrow, and optional capability families are allowed to move at different speeds.',
    principles: [
      {
        kicker: 'default path',
        title: 'The default path sets the bar',
        body: 'reference/standard-service and the stable-root path are the primary surfaces used to judge release readiness.',
      },
      {
        kicker: 'boundaries',
        title: 'x/* does not inherit stability automatically',
        body: 'Extension families are publishable, but they do not receive the same compatibility promise just by existing in the repository.',
      },
      {
        kicker: 'evidence',
        title: 'Compatibility claims follow evidence',
        body: 'Policy, tests, examples, and docs are required before a surface should be treated as frozen for compatibility.',
      },
    ],
    matrixTitle: 'Current support matrix',
    matrixBody:
      'The matrix below is synced from repository facts and is meant to be read as an adoption aid, not a branding grid.',
  },
  zh: {
    title: '发布',
    description: '发布姿态、兼容性承诺与当前支持矩阵。',
    eyebrow: '发布姿态',
    introTitle: '怎么阅读发布姿态。',
    introBody:
      '这个页面的目标是把兼容性边界说清楚。你应该能借它分辨：哪些部分可以沿 canonical path 先采用，哪些能力仍在演进，以及哪些发布目标仍然被策略或测试深度明确卡住。',
    guideCards: [
      {
        kicker: '规范路径',
        title: '先从 canonical path 评估',
        body: '如果你准备真正使用 Plumego，先从 reference app 和已文档化的默认路径开始，不要把所有可选表面都当成同等成熟。',
      },
      {
        kicker: '矩阵',
        title: '把支持矩阵看成边界地图',
        body: '支持矩阵不是营销文案，它告诉你哪些区域承担更强兼容性预期，哪些仍然需要谨慎。',
      },
      {
        kicker: '路线图',
        title: '把发布姿态和路线图野心分开',
        body: '一个包可以很有价值、也在积极迭代，但这并不自动意味着它已经拥有和稳定根一样的稳定性承诺。',
      },
    ],
    principlesTitle: '兼容性原则',
    principlesBody:
      '发布姿态由几条简单规则约束：先看默认路径，稳定根保持收敛，可选能力家族允许按不同速度推进。',
    principles: [
      {
        kicker: '默认路径',
        title: '默认路径决定发布门槛',
        body: 'reference/standard-service 和稳定根路径，是判断当前是否可用的主要依据。',
      },
      {
        kicker: '边界',
        title: 'x/* 不会自动继承稳定性',
        body: '扩展家族可以被发布，但不会仅因为存在于仓库里就自动获得与稳定根相同的兼容性承诺。',
      },
      {
        kicker: '证据',
        title: '兼容性声明必须有证据支撑',
        body: '策略、测试、示例和文档都补齐之后，某个表面才应该被视为真正冻结。',
      },
    ],
    matrixTitle: '当前支持矩阵',
    matrixBody:
      '下面这张矩阵由仓库事实同步而来，应该被当成采用辅助工具，而不是品牌展示列表。',
  },
} as const;

export const SUPPORT_MATRIX = RELEASE_FACTS.supportMatrix;

export const USE_CASES_COPY = {
  en: {
    title: 'Adoption Fit',
    description: 'Use this page to decide whether Plumego matches the service in front of you and which reading path should come next.',
    eyebrow: 'Fit Check',
    introTitle: 'Use this page to decide whether Plumego matches the service in front of you.',
    introBody:
      'Architecture explains how the repository is structured. This page is narrower: it helps you decide whether Plumego is a good fit for the service you are evaluating and which entry path to open next.',
    guideCards: [
      {
        kicker: 'good fit',
        title: 'Boundary-sensitive Go services',
        body: 'Teams that need to review route wiring, handler shape, and ownership decisions directly in code benefit most from the canonical path.',
      },
      {
        kicker: 'default path',
        title: 'Internal services with one obvious layout',
        body: 'reference/standard-service gives new services a shared bootstrap and routing model instead of a repo-specific snowflake structure.',
      },
    ],
    decisionTitle: 'Three quick fit checks',
    decisionBody:
      'If your team wants the framework to disappear into conventions, Plumego may feel too explicit. If you want visible route decisions, one canonical service shape, and explicit maturity signals, it becomes much more compelling.',
    cases: [
      {
        kicker: 'internal api',
        title: 'Internal HTTP services with explicit review paths',
        body: 'Use Plumego when you want route registration, middleware order, and dependency wiring to stay easy to inspect in code review.',
        fitPoints: [
          'service boundaries are important to reviewers',
          'bootstrap and handler ownership should stay obvious',
          'the team already thinks in net/http terms',
        ],
        startHref: '/docs/getting-started',
        startLabel: 'Start with Getting Started',
      },
      {
        kicker: 'platform',
        title: 'Shared platform services that need one canonical shape',
        body: 'Plumego works well when multiple teams need the same service skeleton and you want drift to stay low across new services.',
        fitPoints: [
          'new services should start from one reference layout',
          'application-local wiring should remain explicit',
          'reviewers need a small set of accepted patterns',
        ],
        startHref: '/docs/reference-app',
        startLabel: 'Read the Reference App',
      },
      {
        kicker: 'extensions',
        title: 'Capability work that should stay out of the kernel',
        body: "When your repository has optional transport or product capability layers, Plumego's stable-root versus x/* split gives that work a clearer home.",
        fitPoints: [
          'optional capability work should not redefine the default path',
          'tenant, AI, gateway, or data logic should stay outside stable roots',
          'ownership matters more than convenience wrappers',
        ],
        startHref: '/docs/modules/overview',
        startLabel: 'Open Modules Overview',
      },
      {
        kicker: 'release discipline',
        title: 'Repositories that need explicit maturity signals',
        body: 'Plumego is a better fit when you want release posture and roadmap direction called out separately instead of implied by package existence.',
        fitPoints: [
          'compatibility claims should follow evidence',
          'stable and experimental surfaces should be easy to distinguish',
          'adoption decisions need a published boundary map',
        ],
        startHref: '/releases',
        startLabel: 'Inspect Releases',
      },
    ],
  },
  zh: {
    title: '采用判断',
    description: '用这个页面判断当前服务是否适合 Plumego，以及下一步应该沿哪条阅读路径进入。',
    eyebrow: 'Fit Check',
    introTitle: '用这一页判断 Plumego 是否匹配你眼前的服务。',
    introBody:
      'Architecture 负责解释仓库如何分层；这一页更收敛，只负责帮你判断 Plumego 是否适合当前服务，以及下一步该打开哪条入口路径。',
    guideCards: [
      {
        kicker: 'good fit',
        title: '边界敏感的 Go 服务',
        body: '当团队需要在代码评审里直接看清 route wiring、handler 形态和 ownership 决策时，canonical path 会更有价值。',
      },
      {
        kicker: 'default path',
        title: '需要统一起步形态的内部服务',
        body: 'reference/standard-service 能给新服务提供共同的 bootstrap 与 routing 模型，而不是每个仓库自己长一套目录变体。',
      },
    ],
    decisionTitle: '三个快速判断问题',
    decisionBody:
      '如果你的团队更希望框架藏进约定里，Plumego 可能会显得过于显式。相反，如果你想要可见的路由决策、统一服务形态与显式成熟度信号，它会更有吸引力。',
    cases: [
      {
        kicker: 'internal api',
        title: '需要显式评审路径的内部 HTTP 服务',
        body: '当你希望在代码评审中直接看清 route 注册、中间件顺序和依赖 wiring 时，Plumego 很合适。',
        fitPoints: [
          'reviewer 很关注服务边界',
          'bootstrap 与 handler ownership 需要保持直观',
          '团队本来就按 net/http 心智在思考',
        ],
        startHref: '/zh/docs/getting-started',
        startLabel: '从开始使用进入',
      },
      {
        kicker: 'platform',
        title: '需要统一骨架的共享平台服务',
        body: '当多个团队需要使用同一套服务骨架，而且你希望新服务之间的结构漂移尽量小，Plumego 会更顺手。',
        fitPoints: [
          '新服务应该从同一参考布局起步',
          '应用本地 wiring 需要继续显式',
          'review 只想接受一小组明确模式',
        ],
        startHref: '/zh/docs/reference-app',
        startLabel: '阅读参考应用',
      },
      {
        kicker: 'extensions',
        title: '应当留在内核之外的能力工作',
        body: '当仓库里存在可选 transport 或产品能力层时，Plumego 的 stable-root 与 x/* 分层能给这类工作更清晰的归属。',
        fitPoints: [
          '可选能力不应该重定义默认路径',
          'tenant、AI、gateway、data 逻辑应留在稳定根之外',
          'ownership 比便捷包装更重要',
        ],
        startHref: '/zh/docs/modules/overview',
        startLabel: '查看模块总览',
      },
      {
        kicker: 'release discipline',
        title: '需要显式成熟度信号的仓库',
        body: '如果你希望把 release posture 和 roadmap ambition 明确分开，而不是看到包存在就默认稳定，Plumego 更匹配。',
        fitPoints: [
          '兼容性声明需要证据支撑',
          '稳定与实验表面应该容易区分',
          '采用决策需要一张公开的边界图',
        ],
        startHref: '/zh/releases',
        startLabel: '查看发布页',
      },
    ],
  },
} as const;

export const EXAMPLES_COPY = {
  en: {
    title: 'Examples',
    description: 'The example paths Plumego can stand behind today, from the canonical runnable service to guided boundary-reading examples.',
    eyebrow: 'Practical Paths',
    introTitle: 'One canonical service, then five focused recipe tracks.',
    introBody:
      'The examples path has one runnable baseline and five guided recipe tracks. Start from reference/standard-service to see the default service shape, then pick a recipe to answer one concrete question — request flow, module fit, maturity check, or repository shape — before branching further.',
    guideCards: [
      {
        kicker: 'runnable',
        title: 'One canonical runnable example',
        body: 'reference/standard-service is the example Plumego expects users to run first and compare against when creating a new service.',
      },
      {
        kicker: 'guided',
        title: 'Guided examples explain the repository around it',
        body: 'Request flow, module boundaries, and release posture are examples of how to navigate the repository rather than extra mini-apps.',
      },
      {
        kicker: 'honest scope',
        title: 'The site only promotes examples the repo can actually support',
        body: 'This page is intentionally narrower than a framework showcase. It favors examples that reinforce the canonical path instead of distracting from it.',
      },
    ],
    runnableTitle: 'Runnable now',
    runnableBody:
      'If you want one example to clone, run, and compare against your own service, use the reference app first.',
    runnableExamples: [
      {
        kicker: 'canonical',
        title: 'reference/standard-service',
        body: 'The default app layout for bootstrap, route wiring, and transport-only handlers.',
        details: [
          'best starting point for new services',
          'shows the default directory shape',
          'maps directly to docs and validation guidance',
        ],
        href: '/docs/reference-app',
        label: 'Open the reference app guide',
      },
    ],
    guidedTitle: 'Guided examples',
    guidedBody:
      "These examples are not separate runnable apps. They are release-grade walkthroughs that teach the repository's intended reading path and ownership rules.",
    guidedExamples: [
      {
        kicker: 'request path',
        title: 'Read one request from route to write path',
        body: 'Use the request-flow page when you need to trace where HTTP work begins and where transport responsibility should end.',
        href: '/docs/concepts/request-flow',
        label: 'Open request flow',
      },
      {
        kicker: 'module fit',
        title: 'Classify a change before opening packages',
        body: 'Use modules overview, stable roots, and x/* family pages to decide whether work belongs in the stable surface or an extension family.',
        href: '/docs/modules/overview',
        label: 'Open modules overview',
      },
      {
        kicker: 'maturity',
        title: 'Check whether an area is stable enough to adopt',
        body: 'Use release posture and the public releases page to keep compatibility assumptions explicit.',
        href: '/docs/release-posture',
        label: 'Open release posture',
      },
    ],
    advancedTitle: 'Advanced reference apps',
    advancedBody:
      'These reference apps demonstrate capability-specific service shapes. Each one extends reference/standard-service by adding one x/* family. Read the canonical reference app first, then open one of these when you are ready to evaluate a specific capability.',
    advancedExamples: [
      {
        kicker: 'ai service',
        title: 'reference/with-ai',
        body: 'Multi-provider AI with streaming responses and tool routing. Adds x/ai to the stable HTTP kernel without altering the canonical route shape.',
        maturity: 'x/ai — Experimental',
        details: [
          'provider abstraction separate from HTTP transport',
          'streaming responses through standard net/http handlers',
          'stable roots stay compatible as AI API shapes evolve',
        ],
        href: '/docs/modules/x/ai',
        label: 'Open x/ai primer',
        secondaryHref: '/releases',
        secondaryLabel: 'Check maturity posture',
      },
      {
        kicker: 'multi-tenant saas',
        title: 'reference/with-tenant',
        body: 'Per-tenant routing, quota enforcement, and JWT-backed policy evaluation. Adds x/tenant outside the HTTP kernel so stable roots stay compatible when tenant logic evolves.',
        maturity: 'x/tenant — Beta',
        details: [
          'tenant identity from JWT or header, evaluated at the transport layer',
          'per-tenant quota and policy separate from route wiring',
          'stable roots carry the HTTP baseline; x/tenant carries the tenant layer',
        ],
        href: '/docs/modules/x/tenant',
        label: 'Open x/tenant primer',
        secondaryHref: '/releases',
        secondaryLabel: 'Check maturity posture',
      },
    ],
    referenceMatrixTitle: 'All capability reference apps',
    referenceMatrixBody:
      'Each app adds one x/* family to the standard-service baseline. Read the canonical reference app first, then open the one that matches your capability.',
    referenceMatrix: [
      { name: 'reference/standard-service', kicker: 'stable roots', description: 'Canonical service shape — stable-root-only wiring, the default starting layout', href: '/docs/reference-app', maturity: 'Canonical' },
      { name: 'reference/benchmark', kicker: 'stable roots', description: 'Router benchmark harness — compares Plumego\'s router against ServeMux, chi, and gin', href: '/docs/reference-app', maturity: 'Supported reference' },
      { name: 'reference/with-ai', kicker: 'x/ai', description: 'Multi-provider AI with streaming responses and tool routing', href: '/docs/modules/x/ai', maturity: 'Experimental' },
      { name: 'reference/with-tenant', kicker: 'x/tenant', description: 'Per-tenant routing, quota enforcement, and JWT-backed policy', href: '/docs/modules/x/tenant', maturity: 'Beta' },
      { name: 'reference/with-tenant-admin', kicker: 'x/tenant', description: 'Multi-tenant admin plane: lifecycle, quota admin, and usage recording', href: '/docs/modules/x/tenant', maturity: 'Beta' },
      { name: 'reference/with-gateway', kicker: 'x/gateway', description: 'Edge proxy, load balancing, and route rewriting', href: '/docs/modules/x/gateway', maturity: 'Beta' },
      { name: 'reference/with-rest', kicker: 'x/rest', description: 'CRUD resource controllers and REST conventions', href: '/docs/modules/x/rest', maturity: 'Beta' },
      { name: 'reference/with-websocket', kicker: 'x/websocket', description: 'WebSocket real-time transport', href: '/docs/modules/x/websocket', maturity: 'Beta' },
      { name: 'reference/with-messaging', kicker: 'x/messaging', description: 'Async message publishing and subscription wiring', href: '/docs/modules/x/messaging', maturity: 'Beta' },
      { name: 'reference/with-events', kicker: 'x/messaging', description: 'In-process pubsub, idempotent consumption, delayed retry, and webhook delivery', href: '/docs/modules/x/messaging', maturity: 'Experimental' },
      { name: 'reference/with-rpc', kicker: 'x/rpc', description: 'gRPC server with HTTP gateway mount via x/rpc/server and x/rpc/gateway', href: '/docs/modules/x/rpc', maturity: 'Experimental' },
      { name: 'reference/with-webhook', kicker: 'x/messaging/webhook', description: 'Webhook receiver with signature verification', href: '/docs/modules/x/messaging/webhook', maturity: 'Experimental' },
      { name: 'reference/with-observability', kicker: 'x/observability', description: 'Prometheus metrics and OpenTelemetry distributed tracing', href: '/docs/modules/x/observability', maturity: 'Beta' },
      { name: 'reference/with-ops', kicker: 'x/observability/ops', description: 'Protected admin and operations surfaces', href: '/docs/modules/x/observability/ops', maturity: 'Experimental' },
      { name: 'reference/with-frontend', kicker: 'x/frontend', description: 'Static and embedded SPA serving with cache headers, SPA fallback, and API co-location', href: '/docs/modules/x/frontend', maturity: 'Beta' },
      { name: 'reference/production-service', kicker: 'stable roots', description: 'Production-hardened variant with full lifecycle, TLS, and tests', href: '/docs/reference-app', maturity: 'Supported reference' },
    ],
    workerfleetTitle: 'Depth-oriented use case: use-cases/workerfleet',
    workerfleetBody:
      'use-cases/workerfleet is a work-in-progress reference app (not a production-readiness claim) exploring what Plumego looks like at greater depth than the canonical service — distributed worker fleet management with domain models, MongoDB-backed stores, Kubernetes pod discovery, Prometheus metrics, and an alert/notification pipeline. Some service methods still return `ErrNotImplemented`; treat it as a partial reference for worker-monitoring ideas, not a template to copy wholesale.',
    workerfleetDetails: [
      'domain-driven design: task, worker, pod, alert, and event models',
      'MongoDB stores with index management and integration tests',
      'Kubernetes watch-based pod sync and discovery',
      'Prometheus metrics with custom collectors and Grafana dashboards',
      'alert engine with deduplication, threshold rules, and notifiers (Feishu, webhook)',
    ],
    workerfleetMaturity: 'Use case — work in progress',
    workerfleetLabel: 'Read workerfleet README',
  },
  zh: {
    title: '示例',
    description: 'Plumego 当前真正能站得住的示例路径：从 canonical 可运行服务，到围绕它展开的引导式示例。',
    eyebrow: 'Practical Paths',
    introTitle: '一个 canonical 服务，加五条聚焦的 recipe 路径。',
    introBody:
      '示例路径包含一个可运行的基线，以及五条引导式 recipe。先从 reference/standard-service 看默认服务形态，再按需选一条 recipe 回答具体问题——请求路径、模块归属、成熟度确认或仓库分层——然后再向更深的方向展开。',
    guideCards: [
      {
        kicker: 'runnable',
        title: '只有一个 canonical 可运行示例',
        body: 'reference/standard-service 是 Plumego 期望用户最先运行、也最先拿来对照新服务的示例。',
      },
      {
        kicker: 'guided',
        title: '引导式示例解释它周围的仓库结构',
        body: '请求路径、模块边界和发布姿态这些页面，本质上是教你如何读仓库，而不是再做一堆迷你应用。',
      },
      {
        kicker: 'honest scope',
        title: '站点只推广仓库真正能支持的示例',
        body: '这页会比传统框架 showcase 更窄，因为它优先强化 canonical path，而不是分散注意力。',
      },
    ],
    runnableTitle: '现在就能跑的示例',
    runnableBody:
      '如果你只准备跑一个示例，并把它当成自己服务的对照物，就先用 reference app。',
    runnableExamples: [
      {
        kicker: 'canonical',
        title: 'reference/standard-service',
        body: '默认应用布局，覆盖 bootstrap、route wiring 和 transport-only handler。',
        details: [
          '最适合作为新服务起点',
          '展示默认目录结构',
          '可以直接映射到文档与验证路径',
        ],
        href: '/zh/docs/reference-app',
        label: '打开参考应用说明',
      },
    ],
    guidedTitle: '引导式示例',
    guidedBody:
      '这些示例不是额外的可运行应用，而是发布级 walkthrough，用来教你理解这套仓库的默认阅读路径和 ownership 规则。',
    guidedExamples: [
      {
        kicker: 'request path',
        title: '从 route 到写回路径读一遍请求',
        body: '当你需要追踪 HTTP 工作从哪里开始、transport 责任在哪里结束时，就先看 request-flow 页面。',
        href: '/zh/docs/concepts/request-flow',
        label: '打开 Request Flow',
      },
      {
        kicker: 'module fit',
        title: '在翻包前先判断改动归属',
        body: '用 modules overview、stable roots 和 x/* family 页面先决定工作应该落在稳定表面还是扩展家族。',
        href: '/zh/docs/modules/overview',
        label: '打开模块总览',
      },
      {
        kicker: 'maturity',
        title: '先判断成熟度，再决定是否采用',
        body: '用 release posture 和发布页保持兼容性假设显式，而不是默认它已经稳定。',
        href: '/zh/docs/release-posture',
        label: '打开发布姿态',
      },
    ],
    advancedTitle: '进阶参考应用',
    advancedBody:
      '这些参考应用演示了特定能力的服务形态，每一个都在 reference/standard-service 的基础上加入一个 x/* 家族。先读 canonical 参考应用，再根据你需要评估的具体能力选择进入哪一个。',
    advancedExamples: [
      {
        kicker: 'ai service',
        title: 'reference/with-ai',
        body: '带多 provider 抽象、streaming 响应和 tool 路由的 AI 服务。在稳定 HTTP 内核基础上加入 x/ai，不改变 canonical route 结构。',
        maturity: 'x/ai — 实验性',
        details: [
          'provider 抽象与 HTTP transport 分离',
          'streaming 响应通过标准 net/http handler 处理',
          'AI API 形态演进时，稳定根保持兼容',
        ],
        href: '/zh/docs/modules/x/ai',
        label: '打开 x/ai 模块手册',
        secondaryHref: '/zh/releases',
        secondaryLabel: '查看成熟度姿态',
      },
      {
        kicker: 'multi-tenant saas',
        title: 'reference/with-tenant',
        body: '带 per-tenant 路由、配额执行和 JWT 策略评估的多租户服务。x/tenant 在 HTTP 内核之外承接租户层，稳定根不受租户逻辑演进影响。',
        maturity: 'x/tenant — Beta',
        details: [
          '租户身份来自 JWT 或 header，在传输层评估',
          'per-tenant 配额与策略与 route wiring 分开',
          '稳定根承担 HTTP 基线，x/tenant 承担租户层',
        ],
        href: '/zh/docs/modules/x/tenant',
        label: '打开 x/tenant 模块手册',
        secondaryHref: '/zh/releases',
        secondaryLabel: '查看成熟度姿态',
      },
    ],
    referenceMatrixTitle: '所有能力参考应用',
    referenceMatrixBody:
      '每个参考应用都在 standard-service 的基础上加入一个 x/* 家族。先读 canonical 参考应用，再按你需要评估的能力选择进入。',
    referenceMatrix: [
      { name: 'reference/standard-service', kicker: 'stable roots', description: 'canonical 服务形态 — 仅使用稳定根 wiring，默认起步布局', href: '/zh/docs/reference-app', maturity: 'canonical' },
      { name: 'reference/benchmark', kicker: 'stable roots', description: '路由器基准测试 — 与 ServeMux、chi、gin 的对比', href: '/zh/docs/reference-app', maturity: '受支持参考' },
      { name: 'reference/with-ai', kicker: 'x/ai', description: '带 streaming 响应与 tool 路由的多 provider AI 服务', href: '/zh/docs/modules/x/ai', maturity: '实验性' },
      { name: 'reference/with-tenant', kicker: 'x/tenant', description: 'Per-tenant 路由、配额执行与 JWT 策略评估', href: '/zh/docs/modules/x/tenant', maturity: 'Beta' },
      { name: 'reference/with-tenant-admin', kicker: 'x/tenant', description: '多租户管理平面：生命周期、配额管理与用量记录', href: '/zh/docs/modules/x/tenant', maturity: 'Beta' },
      { name: 'reference/with-gateway', kicker: 'x/gateway', description: '边缘代理、负载均衡与路由重写', href: '/zh/docs/modules/x/gateway', maturity: 'Beta' },
      { name: 'reference/with-rest', kicker: 'x/rest', description: 'CRUD 资源控制器与 REST 规范', href: '/zh/docs/modules/x/rest', maturity: 'Beta' },
      { name: 'reference/with-websocket', kicker: 'x/websocket', description: 'WebSocket 实时传输', href: '/zh/docs/modules/x/websocket', maturity: 'Beta' },
      { name: 'reference/with-messaging', kicker: 'x/messaging', description: '异步消息发布与订阅接线', href: '/zh/docs/modules/x/messaging', maturity: 'Beta' },
      { name: 'reference/with-events', kicker: 'x/messaging', description: '进程内 pubsub、幂等消费、延迟重试与 webhook 投递', href: '/zh/docs/modules/x/messaging', maturity: '实验性' },
      { name: 'reference/with-rpc', kicker: 'x/rpc', description: '通过 x/rpc/server 托管 gRPC 服务并经 x/rpc/gateway 挂载 HTTP 端点', href: '/zh/docs/modules/x/rpc', maturity: '实验性' },
      { name: 'reference/with-webhook', kicker: 'x/messaging/webhook', description: '带签名校验的 Webhook 接收器', href: '/zh/docs/modules/x/messaging/webhook', maturity: '实验性' },
      { name: 'reference/with-observability', kicker: 'x/observability', description: 'Prometheus 指标与 OpenTelemetry 分布式追踪', href: '/zh/docs/modules/x/observability', maturity: 'Beta' },
      { name: 'reference/with-ops', kicker: 'x/observability/ops', description: '受保护的管理与运维表面', href: '/zh/docs/modules/x/observability/ops', maturity: '实验性' },
      { name: 'reference/with-frontend', kicker: 'x/frontend', description: '静态与内嵌 SPA 服务，支持缓存头、SPA fallback 与 API 同挂载', href: '/zh/docs/modules/x/frontend', maturity: 'Beta' },
      { name: 'reference/production-service', kicker: 'stable roots', description: '带完整生命周期、TLS 和测试的生产级加固变体', href: '/zh/docs/reference-app', maturity: '受支持参考' },
    ],
    workerfleetTitle: '深度参考用例：use-cases/workerfleet',
    workerfleetBody:
      'use-cases/workerfleet 是一个仍在推进中的参考应用（不是生产就绪声明），用来探索 Plumego 在超出 canonical 服务深度时的形态——分布式 worker 集群管理，包含领域模型、MongoDB 存储、Kubernetes Pod 发现、Prometheus 指标以及告警/通知流水线。部分服务方法仍返回 `ErrNotImplemented`；应把它当作 worker 监控思路的部分参考，而不是可以整体照搬的模板。',
    workerfleetDetails: [
      '领域驱动设计：task、worker、pod、alert 和 event 模型',
      'MongoDB 存储，含索引管理和集成测试',
      'Kubernetes watch-based pod 同步与发现',
      'Prometheus 指标，含自定义 collector 和 Grafana 看板',
      '告警引擎，含去重、阈值规则和通知器（飞书、webhook）',
    ],
    workerfleetMaturity: '用例 — 进行中',
    workerfleetLabel: '阅读 workerfleet README',
  },
} as const;

export const STABILITY_COPY = {
  en: {
    title: 'Stability',
    description: 'Which modules you can depend on today, which are frozen between release refs, and which still require evaluation before adopting.',
    eyebrow: 'Module Stability',
    heroTitle: 'Know what you can rely on before you build.',
    heroBody: 'Plumego separates its surface into four tiers. The 9 stable roots are the only surfaces with a long-term compatibility promise. Everything else has a label that tells you exactly how much to trust it.',
    tiers: [
      {
        status: 'stable',
        label: 'Stable roots',
        badge: 'stable',
        promise: 'v1 stable-root compatibility promise. Safe for production.',
        adopt: 'Adopt now — these are the recommended starting point for every service.',
        modules: ['core', 'router', 'contract', 'middleware', 'security', 'store', 'health', 'log', 'metrics'],
      },
      {
        status: 'supported',
        label: 'Supported reference',
        badge: 'supported',
        promise: 'Aligned with the canonical path. Read as a guide.',
        adopt: 'Use reference/standard-service as your starting template.',
        modules: ['reference/standard-service', 'cmd/plumego'],
      },
      {
        status: 'beta',
        label: 'Beta extensions',
        badge: 'beta',
        promise: 'API frozen between minor release refs.',
        adopt: 'Safe to adopt with awareness — check release notes before upgrades.',
        modules: ['x/rest', 'x/gateway', 'x/websocket', 'x/observability', 'x/tenant', 'x/frontend', 'x/messaging'],
      },
      {
        status: 'experimental',
        label: 'Experimental extensions',
        badge: 'experimental',
        promise: 'API may change. Evaluate deliberately before adopting.',
        adopt: 'Adopt for clear reasons after reading the module primer and maturity evidence.',
        modules: ['x/ai', 'x/data', 'x/fileapi', 'x/openapi', 'x/resilience', 'x/rpc', 'x/validate', 'x/data/cache', 'x/gateway/discovery', 'x/gateway/ipc', 'x/messaging/mq', 'x/messaging/pubsub', 'x/messaging/scheduler', 'x/messaging/webhook', 'x/observability/devtools', 'x/observability/ops'],
      },
    ],
    promotionTitle: 'How modules get promoted',
    promotionBody: 'A module does not become stable by declaration. Beta requires two consecutive tagged release refs with no API changes, release-backed snapshots, and owner sign-off.',
    promotionSteps: [
      { label: '01', title: 'Two release refs', body: 'No exported symbol may change across two consecutive tagged release refs.' },
      { label: '02', title: 'Release-backed snapshots', body: 'API snapshots are recorded at both refs and compared automatically by CI.' },
      { label: '03', title: 'Owner sign-off', body: 'The module owner confirms compatibility obligations in writing before the status field changes.' },
    ],
    ctaTitle: 'Ready to build?',
    ctaBody: 'Start from the reference app, verify your module choices against the stability matrix, then expand.',
    ctaPrimary: { label: 'Get Started', href: '/docs/getting-started' },
    ctaSecondary: { label: 'Full release matrix', href: '/releases' },
  },
  zh: {
    title: '稳定性',
    description: '哪些模块今天可以依赖，哪些在发布 ref 间冻结，哪些在采用前还需要评估。',
    eyebrow: '模块稳定性',
    heroTitle: '动手之前，先搞清楚能靠哪些。',
    heroBody: 'Plumego 把表面分成四个层级。9 个稳定根是唯一有长期兼容性承诺的部分，其他所有内容都有标签明确告诉你能信任多少。',
    tiers: [
      {
        status: 'stable',
        label: '稳定根',
        badge: '稳定',
        promise: 'v1 稳定根兼容性承诺，可用于生产。',
        adopt: '立即采用——这是所有服务推荐的起点。',
        modules: ['core', 'router', 'contract', 'middleware', 'security', 'store', 'health', 'log', 'metrics'],
      },
      {
        status: 'supported',
        label: '受支持参考',
        badge: '受支持',
        promise: '与 canonical path 保持同步，作为指南阅读。',
        adopt: '以 reference/standard-service 作为起步模板。',
        modules: ['reference/standard-service', 'cmd/plumego'],
      },
      {
        status: 'beta',
        label: 'Beta 扩展',
        badge: 'beta',
        promise: 'minor 发布 ref 间 API 冻结。',
        adopt: '可以采用，但升级前需要查看 release notes。',
        modules: ['x/rest', 'x/gateway', 'x/websocket', 'x/observability', 'x/tenant', 'x/frontend', 'x/messaging'],
      },
      {
        status: 'experimental',
        label: '实验性扩展',
        badge: '实验性',
        promise: 'API 可能变化，采用前请评估。',
        adopt: '在阅读模块手册和成熟度证据后，有明确理由时再采用。',
        modules: ['x/ai', 'x/data', 'x/fileapi', 'x/openapi', 'x/resilience', 'x/rpc', 'x/validate', 'x/data/cache', 'x/gateway/discovery', 'x/gateway/ipc', 'x/messaging/mq', 'x/messaging/pubsub', 'x/messaging/scheduler', 'x/messaging/webhook', 'x/observability/devtools', 'x/observability/ops'],
      },
    ],
    promotionTitle: '模块如何晋级',
    promotionBody: '模块不能靠声明变稳定。Beta 晋级要求：连续两个 release ref 内没有 API 变化、有 release-backed 快照，以及负责人书面签字。',
    promotionSteps: [
      { label: '01', title: '两个 release ref', body: '在连续两个 release ref 之间，不得有任何 exported symbol 变化。' },
      { label: '02', title: 'Release-backed 快照', body: '在两个 ref 分别记录 API 快照，由 CI 自动比对。' },
      { label: '03', title: '负责人签字', body: '模块负责人在状态字段变更前书面确认兼容性义务。' },
    ],
    ctaTitle: '准备好动手了？',
    ctaBody: '从参考应用起步，对照稳定性矩阵确认模块选择，然后再扩展。',
    ctaPrimary: { label: '开始使用', href: '/zh/docs/getting-started' },
    ctaSecondary: { label: '完整发布矩阵', href: '/zh/releases' },
  },
} as const;

export const NOT_FOUND_COPY = {
  en: {
    title: 'Page Not Found',
    description: 'The page you requested does not exist or has moved. Start again from docs, the reference app, or the release pages.',
    eyebrow: '404',
    body:
      'The URL may have moved while the website structure was being tightened. Use one of the primary entry points below instead of guessing deeper paths.',
    actions: [
      { label: 'Go to Docs', href: '/docs' },
      { label: 'Open Getting Started', href: '/docs/getting-started' },
      { label: 'View Releases', href: '/releases' },
      { label: '切换到中文', href: '/zh' },
    ],
  },
  zh: {
    title: '页面不存在',
    description: '你访问的页面不存在，或者路径已经调整。请从文档、参考应用或发布页重新进入。',
    eyebrow: '404',
    body:
      '站点结构在收敛过程中，一部分路径可能已经调整。不要继续猜更深的 URL，直接从下面这些主入口重新进入。',
    actions: [
      { label: '进入文档', href: '/zh/docs' },
      { label: '打开开始使用', href: '/zh/docs/getting-started' },
      { label: '查看发布页', href: '/zh/releases' },
      { label: 'Switch to English', href: '/' },
    ],
  },
} as const;

export const ECOSYSTEM_COPY = {
  en: {
    title: 'Ecosystem',
    description:
      'All x/* capability families — grouped by maturity. Each family has a clear scenario, a concrete use case, and a link to the module docs.',
    eyebrow: 'Extension Families',
    introTitle: 'Pick the right capability family, not the loudest one.',
    introBody:
      'Plumego keeps its stable kernel small and pushes optional capability into x/* families. This page gives you a scenario-first view of every family so you can match your need to the right package — and know exactly what level of maturity you are accepting.',
    betaTitle: 'Beta — API frozen between release refs',
    betaBody:
      'These families have API snapshots, owner sign-off, and promotion evidence. They are safe to evaluate for production, with the understanding that the API may change between major release refs.',
    experimentalTitle: 'Experimental — evaluate before adopting',
    experimentalBody:
      'These families are included in repo quality gates and tested, but their API surface is not frozen. Start with the family entrypoint and expect changes.',
    betaFamilies: [
      {
        name: 'x/rest',
        scenario: 'REST resource APIs',
        useCase: 'Build CRUD resources with a typed resource interface, consistent response envelopes, and validation hooks — without reinventing the pattern per endpoint.',
        maturity: 'beta',
        docsHref: '/docs/modules/x/rest',
      },
      {
        name: 'x/gateway',
        scenario: 'API gateway & proxy',
        useCase: 'Route and rewrite HTTP traffic between upstream services. Includes dynamic service discovery, load balancing, and edge rewrite rules.',
        maturity: 'beta',
        docsHref: '/docs/modules/x/gateway',
      },
      {
        name: 'x/websocket',
        scenario: 'Real-time WebSocket',
        useCase: 'Manage connected WebSocket clients with a Hub model. Broadcast messages, handle disconnect, and keep transport concerns out of your business logic.',
        maturity: 'beta',
        docsHref: '/docs/modules/x/websocket',
      },
      {
        name: 'x/observability',
        scenario: 'Observability & ops tooling',
        useCase: 'Attach structured metrics, trace exporters, and dev-mode diagnostics without touching the stable request path. Includes devtools and ops sub-packages.',
        maturity: 'beta',
        docsHref: '/docs/modules/x/observability',
      },
      {
        name: 'x/tenant',
        scenario: 'Multi-tenant SaaS',
        useCase: 'Resolve tenant identity at the transport layer and enforce per-tenant policy without letting tenant logic leak into stable roots or handler code.',
        maturity: 'beta',
        docsHref: '/docs/modules/x/tenant',
      },
      {
        name: 'x/frontend',
        scenario: 'Frontend asset serving',
        useCase: 'Serve static assets, embed SPA shells, and handle cache-busting strategies as a transport-layer concern — without polluting handler or business logic.',
        maturity: 'beta',
        docsHref: '/docs/modules/x/frontend',
      },
      {
        name: 'x/messaging',
        scenario: 'Messaging, queues & webhooks',
        useCase: 'Connect message queues, publish/subscribe flows, scheduled jobs, and inbound or outbound webhook transport — all under one family entrypoint. Subordinate primitives (mq, pubsub, scheduler, webhook) remain experimental.',
        maturity: 'beta',
        docsHref: '/docs/modules/x/messaging',
      },
    ],
    experimentalFamilies: [
      {
        name: 'x/ai',
        scenario: 'AI streaming & tool routing',
        useCase: 'Build AI streaming SSE endpoints with provider contracts, session state management, and explicit tool invocation policy — without coupling AI logic to the HTTP kernel. Stable-tier subpackages (provider, session, streaming, tool) have beta evidence.',
        maturity: 'experimental',
        docsHref: '/docs/modules/x/ai',
      },
      {
        name: 'x/data',
        scenario: 'Data access & storage',
        useCase: 'Add structured data access primitives on top of the store stable root — typed queries, migrations, and repository patterns for common persistence scenarios. x/data/file and x/data/idempotency have beta surface evidence.',
        maturity: 'experimental',
        docsHref: '/docs/modules/x/data',
      },
      {
        name: 'x/fileapi',
        scenario: 'File upload & download',
        useCase: 'Handle multipart uploads, chunked downloads, and temporary signed URL generation — separated cleanly from the HTTP kernel.',
        maturity: 'experimental',
        docsHref: '/docs/modules/x/fileapi',
      },
      {
        name: 'x/openapi',
        scenario: 'OpenAPI document generation',
        useCase: 'Generate OpenAPI 3.1 specifications from registered routes and operation hints — dependency-free JSON/YAML output wired through the CLI.',
        maturity: 'experimental',
        docsHref: '/docs/modules/x/openapi',
      },
      {
        name: 'x/resilience',
        scenario: 'Resilience & circuit breaking',
        useCase: 'Wrap upstream calls with retry logic, circuit breakers, and timeout policies — composable primitives that stay outside the stable transport layer.',
        maturity: 'experimental',
        docsHref: '/docs/modules/x/resilience',
      },
      {
        name: 'x/rpc',
        scenario: 'gRPC server and gateway',
        useCase: 'Optional gRPC server lifecycle helpers, outbound connection pooling, and HTTP-over-RPC gateway adapters alongside standard HTTP routes.',
        maturity: 'experimental',
        docsHref: '/docs/modules/x/rpc',
      },
      {
        name: 'x/validate',
        scenario: 'Request validation',
        useCase: 'Explicit BindJSON and Bind call sites in handlers that decode JSON and return structured contract.APIError responses — no tag-based global validator.',
        maturity: 'experimental',
        docsHref: '/docs/modules/x/validate',
      },
    ],
    maturityNote: 'Beta families have API snapshots and promotion evidence. Experimental families are tested but not API-frozen. See',
    maturityNoteLink: { label: 'release posture', href: '/releases' },
    maturityNoteSuffix: 'for details.',
    guideTitle: 'How to choose the right family',
    guideSteps: [
      { label: '01', title: 'Name your scenario', body: 'Start with what you are building — real-time, multi-tenant, AI, file handling — not which package name sounds right.' },
      { label: '02', title: 'Check the maturity tier', body: 'Beta families are safe to evaluate for production with known API stability. Experimental families may change between minor versions.' },
      { label: '03', title: 'Read the family entrypoint', body: 'Each x/* family has a primary package. Start there before exploring sub-packages like x/messaging/mq or x/gateway/discovery.' },
    ],
  },
  zh: {
    title: '生态系统',
    description: '所有 x/* 能力家族——按成熟度分组。每个家族都有清晰的使用场景、具体用例和模块文档链接。',
    eyebrow: '扩展家族',
    introTitle: '选对能力家族，而不是选最热门的那个。',
    introBody:
      'Plumego 保持稳定内核精简，把可选能力推入 x/* 家族。这个页面以场景为优先，呈现所有家族，让你能把需求匹配到正确的包——并清楚知道自己接受的是哪个成熟度等级。',
    betaTitle: 'Beta — 版本引用间 API 冻结',
    betaBody:
      '这些家族有 API 快照、Owner 确认书和晋级证据。可以放心用于生产评估，但需了解 API 可能在主版本引用之间有所变化。',
    experimentalTitle: 'Experimental — 采用前请先评估',
    experimentalBody:
      '这些家族纳入仓库质量门禁并有测试，但 API surface 尚未冻结。从家族入口点开始，预期会有变更。',
    betaFamilies: [
      {
        name: 'x/rest',
        scenario: 'REST 资源 API',
        useCase: '使用类型化资源接口、统一响应封装和验证钩子构建 CRUD 资源——无需在每个端点重新发明模式。',
        maturity: 'beta',
        docsHref: '/zh/docs/modules/x/rest',
      },
      {
        name: 'x/gateway',
        scenario: 'API 网关与代理',
        useCase: '在上游服务间路由和重写 HTTP 流量。包括动态服务发现、负载均衡和边缘重写规则。',
        maturity: 'beta',
        docsHref: '/zh/docs/modules/x/gateway',
      },
      {
        name: 'x/websocket',
        scenario: '实时 WebSocket',
        useCase: '用 Hub 模型管理 WebSocket 连接。广播消息、处理断连，保持传输关注点与业务逻辑分离。',
        maturity: 'beta',
        docsHref: '/zh/docs/modules/x/websocket',
      },
      {
        name: 'x/observability',
        scenario: '可观测性与运维工具',
        useCase: '附加结构化指标、链路导出器和开发模式诊断工具——不影响稳定请求路径。包含 devtools 和 ops 子包。',
        maturity: 'beta',
        docsHref: '/zh/docs/modules/x/observability',
      },
      {
        name: 'x/tenant',
        scenario: '多租户 SaaS',
        useCase: '在传输层解析租户身份并执行每租户策略——不让租户逻辑泄漏到稳定根或 handler 代码。',
        maturity: 'beta',
        docsHref: '/zh/docs/modules/x/tenant',
      },
      {
        name: 'x/frontend',
        scenario: '前端资产服务',
        useCase: '提供静态资产服务、嵌入 SPA shell、处理缓存清除策略——作为传输层关注点，不污染 handler 或业务逻辑。',
        maturity: 'beta',
        docsHref: '/zh/docs/modules/x/frontend',
      },
      {
        name: 'x/messaging',
        scenario: '消息队列与 Webhook',
        useCase: '连接消息队列、发布/订阅流、定时任务以及入站或出站 Webhook 传输——统一在一个家族入口点下。下级原语（mq、pubsub、scheduler、webhook）仍为实验性。',
        maturity: 'beta',
        docsHref: '/zh/docs/modules/x/messaging',
      },
    ],
    experimentalFamilies: [
      {
        name: 'x/ai',
        scenario: 'AI 流式与工具路由',
        useCase: '使用 provider contract、session 状态管理和显式工具调用策略构建 AI 流式 SSE 端点——不让 AI 逻辑与 HTTP 内核耦合。稳定层子包（provider、session、streaming、tool）已有 beta 证据。',
        maturity: 'experimental',
        docsHref: '/zh/docs/modules/x/ai',
      },
      {
        name: 'x/data',
        scenario: '数据访问与存储',
        useCase: '在 store 稳定根之上添加结构化数据访问原语——类型化查询、迁移和常见持久化场景的 repository 模式。x/data/file 和 x/data/idempotency 已有 beta surface 证据。',
        maturity: 'experimental',
        docsHref: '/zh/docs/modules/x/data',
      },
      {
        name: 'x/fileapi',
        scenario: '文件上传与下载',
        useCase: '处理 multipart 上传、分片下载和临时签名 URL 生成——与 HTTP 内核完全分离。',
        maturity: 'experimental',
        docsHref: '/zh/docs/modules/x/fileapi',
      },
      {
        name: 'x/openapi',
        scenario: 'OpenAPI 文档生成',
        useCase: '从已注册路由和操作提示生成 OpenAPI 3.1 规范——无依赖的 JSON/YAML 输出，通过 CLI 集成。',
        maturity: 'experimental',
        docsHref: '/zh/docs/modules/x/openapi',
      },
      {
        name: 'x/resilience',
        scenario: '弹性与熔断',
        useCase: '用重试逻辑、熔断器和超时策略包装上游调用——可组合的原语，保持在稳定传输层之外。',
        maturity: 'experimental',
        docsHref: '/zh/docs/modules/x/resilience',
      },
      {
        name: 'x/rpc',
        scenario: 'gRPC 服务与网关',
        useCase: '可选的 gRPC 服务端生命周期助手、出站连接池和 HTTP-over-RPC 网关适配器，与标准 HTTP 路由并行运行。',
        maturity: 'experimental',
        docsHref: '/zh/docs/modules/x/rpc',
      },
      {
        name: 'x/validate',
        scenario: '请求验证',
        useCase: '在 handler 中显式调用 BindJSON 和 Bind，解码 JSON 并返回结构化的 contract.APIError——不使用 tag 驱动的全局验证器。',
        maturity: 'experimental',
        docsHref: '/zh/docs/modules/x/validate',
      },
    ],
    maturityNote: 'Beta 家族有 API 快照和晋级证据。Experimental 家族有测试但 API 未冻结。详见',
    maturityNoteLink: { label: '发布姿态', href: '/zh/releases' },
    maturityNoteSuffix: '。',
    guideTitle: '如何选择合适的家族',
    guideSteps: [
      { label: '01', title: '先描述你的场景', body: '从你要构建什么出发——实时、多租户、AI、文件处理——而不是从哪个包名听起来对。' },
      { label: '02', title: '确认成熟度等级', body: 'Beta 家族可以放心用于生产评估，API 稳定性有保证。Experimental 家族可能在小版本之间有变化。' },
      { label: '03', title: '从家族入口点开始读', body: '每个 x/* 家族都有一个主包。先从那里开始，再进入 x/messaging/mq 或 x/gateway/discovery 等子包。' },
    ],
  },
} as const;
