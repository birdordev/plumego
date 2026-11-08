import { defineConfig } from 'astro/config';
import mdx from '@astrojs/mdx';
import sitemap from '@astrojs/sitemap';
import starlight from '@astrojs/starlight';

/**
 * Determine sitemap priority based on URL path.
 */
function getPriority(path) {
  if (path === '/') return 1.0;
  if (path.startsWith('/docs')) return 0.8;
  return 0.6;
}

/**
 * Determine sitemap changefreq based on URL path.
 */
function getChangefreq(path) {
  if (path === '/') return 'weekly';
  if (path.startsWith('/docs')) return 'weekly';
  return 'monthly';
}

/**
 * Determine sitemap lastmod based on URL path.
 * Docs pages derive from file mtime; landing pages use build time.
 */
async function getLastmod(page) {
  const url = new URL(page);
  const path = url.pathname;

  // Map docs URLs to source files for accurate mtime
  const docsPathMatch = path.match(/^\/zh?\/docs\/(.*)$/);
  if (docsPathMatch) {
    const slug = docsPathMatch[1];
    const candidates = [
      `src/content/docs/docs/${slug}.mdx`,
      `src/content/docs/docs/${slug}.md`,
      `src/content/docs/docs/${slug}/index.mdx`,
      `src/content/docs/docs/${slug}/index.md`,
    ];
    for (const c of candidates) {
      try {
        const { stat } = await import('node:fs/promises');
        const s = await stat(c);
        return s.mtime.toISOString();
      } catch {
        // try next candidate
      }
    }
  }

  return new Date().toISOString();
}

export default defineConfig({
  site: 'https://plumego.birdor.dev',
  output: 'static',
  trailingSlash: 'never',
  integrations: [
    sitemap({
      filter: (page) => {
        const path = new URL(page).pathname;
        // Exclude 404 and any hidden/internal pages
        if (path.includes('404')) return false;
        // Exclude search page (Starlight may generate /search)
        if (path === '/search' || path === '/zh/search') return false;
        return true;
      },
      i18n: {
        defaultLocale: 'en',
        locales: {
          en: 'en',
          zh: 'zh-CN',
        },
      },
      serialize: async (item) => {
        const url = new URL(item.url);
        const path = url.pathname;
        return {
          ...item,
          lastmod: await getLastmod(item.url),
          changefreq: getChangefreq(path),
          priority: getPriority(path),
        };
      },
    }),
    starlight({
      title: 'Plumego',
      description: 'stdlib-only Go HTTP toolkit — explicit by design, agent-ready by structure. Visible routing, narrow stable roots, machine-readable specs.',
      favicon: '/favicon.svg',
      disable404Route: true,
      social: [
        {
          icon: 'github',
          label: 'GitHub',
          href: 'https://github.com/birdordev/plumego',
        },
      ],
      locales: {
        root: {
          label: 'English',
          lang: 'en',
        },
        zh: {
          label: '简体中文',
          lang: 'zh-CN',
        },
      },
      customCss: [
        './src/styles/tokens.css',
        './src/styles/starlight-bridge.css',
        './src/styles/global.css',
        './src/styles/prose.css',
        './src/styles/home.css',
      ],
      components: {
        Head: './src/components/starlight/Head.astro',
        Header: './src/components/starlight/Header.astro',
        ThemeSelect: './src/components/shared/ThemeSelect.astro',
        LanguageSelect: './src/components/shared/LanguageSelect.astro',
        Banner: './src/components/starlight/Banner.astro',
      },
      sidebar: [
        {
          label: 'Start Here',
          translations: { 'zh-CN': '从这里开始' },
          items: [
            { label: 'Introduction', slug: 'docs', translations: { 'zh-CN': '介绍' } },
            { label: 'Getting Started', slug: 'docs/getting-started', translations: { 'zh-CN': '开始使用' } },
            { label: 'Reference App', slug: 'docs/reference-app', translations: { 'zh-CN': '参考应用' } },
            { label: 'Choose Your Path', slug: 'docs/x-family', translations: { 'zh-CN': '选择你的路径' } },
            { label: 'FAQ', slug: 'docs/faq', translations: { 'zh-CN': '常见问题' } },
          ],
        },
        {
          label: 'Core Concepts',
          translations: { 'zh-CN': '核心概念' },
          items: [
            { label: 'Request Flow', slug: 'docs/concepts/request-flow', translations: { 'zh-CN': '请求流程' } },
            { label: 'Middleware Model', slug: 'docs/concepts/middleware-model', translations: { 'zh-CN': '中间件模型' } },
            { label: 'Error Model', slug: 'docs/concepts/error-model', translations: { 'zh-CN': '错误模型' } },
            { label: 'Configuration', slug: 'docs/concepts/configuration-model', translations: { 'zh-CN': '配置模型' } },
            { label: 'Agent-First Workflow', slug: 'docs/concepts/agent-first-workflow', translations: { 'zh-CN': 'Agent 工作流' }, badge: { text: 'Key', variant: 'note' } },
            { label: 'Stable Roots', slug: 'docs/stable-roots', translations: { 'zh-CN': '稳定根' } },
            { label: 'Extension Maturity', slug: 'docs/concepts/extension-maturity', translations: { 'zh-CN': '扩展成熟度' } },
            { label: 'When Not to Use', slug: 'docs/when-not-to-use', translations: { 'zh-CN': '不适用场景' } },
          ],
        },
        {
          label: 'Stable Modules',
          translations: { 'zh-CN': '稳定模块' },
          collapsed: false,
          items: [
            { label: 'Overview', slug: 'docs/modules/overview', translations: { 'zh-CN': '总览' } },
            { label: 'core', slug: 'docs/modules/core' },
            { label: 'contract', slug: 'docs/modules/contract' },
            { label: 'router', slug: 'docs/modules/router' },
            { label: 'middleware', slug: 'docs/modules/middleware' },
            { label: 'security', slug: 'docs/modules/security' },
            { label: 'health', slug: 'docs/modules/health' },
            { label: 'log', slug: 'docs/modules/log' },
            { label: 'metrics', slug: 'docs/modules/metrics' },
            { label: 'store', slug: 'docs/modules/store' },
          ],
        },
        {
          label: 'Extensions (x/*)',
          translations: { 'zh-CN': '扩展模块 (x/*)' },
          items: [
            {
              label: 'Beta',
              translations: { 'zh-CN': 'Beta 级' },
              collapsed: false,
              items: [
                { label: 'x/rest',          slug: 'docs/modules/x-rest',          badge: { text: 'Beta', variant: 'tip' } },
                { label: 'x/gateway',       slug: 'docs/modules/x-gateway',       badge: { text: 'Beta', variant: 'tip' } },
                { label: 'x/websocket',     slug: 'docs/modules/x-websocket',     badge: { text: 'Beta', variant: 'tip' } },
                { label: 'x/observability', slug: 'docs/modules/x-observability', badge: { text: 'Beta', variant: 'tip' } },
                { label: 'x/tenant',        slug: 'docs/modules/x-tenant',        badge: { text: 'Beta', variant: 'tip' } },
                { label: 'x/frontend',      slug: 'docs/modules/x-frontend',      badge: { text: 'Beta', variant: 'tip' } },
                { label: 'x/messaging',     slug: 'docs/modules/x-messaging',     badge: { text: 'Beta', variant: 'tip' } },
              ],
            },
            {
              label: 'Experimental',
              translations: { 'zh-CN': '实验性' },
              collapsed: true,
              items: [
                { label: 'x/ai',                      slug: 'docs/modules/x-ai',                      badge: { text: 'Exp', variant: 'caution' } },
                { label: 'x/data',                    slug: 'docs/modules/x-data',                    badge: { text: 'Exp', variant: 'caution' } },
                { label: 'x/fileapi',                 slug: 'docs/modules/x-fileapi',                 badge: { text: 'Exp', variant: 'caution' } },
                { label: 'x/resilience',              slug: 'docs/modules/x-resilience',              badge: { text: 'Exp', variant: 'caution' } },
                { label: 'x/rpc',                     slug: 'docs/modules/x-rpc',                     badge: { text: 'Exp', variant: 'caution' } },
                { label: 'x/openapi',                 slug: 'docs/modules/x-openapi',                 badge: { text: 'Exp', variant: 'caution' } },
                { label: 'x/validate',                slug: 'docs/modules/x-validate',                badge: { text: 'Exp', variant: 'caution' } },
                { label: 'x/data/cache',              slug: 'docs/modules/x-data-cache',              badge: { text: 'Exp', variant: 'caution' } },
                { label: 'x/messaging (primitives)',  slug: 'docs/modules/x-messaging-subordinates',  badge: { text: 'Exp', variant: 'caution' } },
                { label: 'x/gateway/discovery',       slug: 'docs/modules/x-gateway-discovery',               badge: { text: 'Exp', variant: 'caution' } },
                { label: 'x/gateway/ipc',             slug: 'docs/modules/x-gateway-ipc',             badge: { text: 'Exp', variant: 'caution' } },
                { label: 'x/observability/ops',       slug: 'docs/modules/x-observability-ops',      badge: { text: 'Exp', variant: 'caution' } },
                { label: 'x/observability/devtools',  slug: 'docs/modules/x-observability-devtools', badge: { text: 'Exp', variant: 'caution' } },
                { label: 'x/messaging/scheduler',     slug: 'docs/modules/x-messaging-scheduler',    badge: { text: 'Exp', variant: 'caution' } },
                { label: 'x/messaging/webhook',       slug: 'docs/modules/x-messaging-webhook',      badge: { text: 'Exp', variant: 'caution' } },
              ],
            },
          ],
        },
        {
          label: 'How-to Guides',
          translations: { 'zh-CN': '操作指南' },
          items: [
            {
              label: 'Auth & Security',
              translations: { 'zh-CN': '认证与安全' },
              collapsed: false,
              items: [
                { label: 'Add JWT Auth', slug: 'docs/guides/add-jwt-auth', translations: { 'zh-CN': '添加 JWT 认证' } },
                { label: 'Custom Middleware', slug: 'docs/guides/custom-middleware', translations: { 'zh-CN': '自定义中间件' } },
                { label: 'Handle Errors', slug: 'docs/guides/handle-errors', translations: { 'zh-CN': '错误处理' } },
              ],
            },
            {
              label: 'Build & Test',
              translations: { 'zh-CN': '构建与测试' },
              collapsed: false,
              items: [
                { label: 'Build a REST Resource', slug: 'docs/guides/build-rest-resource', translations: { 'zh-CN': '构建 REST 资源' } },
                { label: 'Testing Handlers', slug: 'docs/guides/testing-handlers', translations: { 'zh-CN': '测试 Handler' } },
                { label: 'Connect Database', slug: 'docs/guides/connect-database', translations: { 'zh-CN': '连接数据库' } },
              ],
            },
            {
              label: 'Capabilities',
              translations: { 'zh-CN': '能力扩展' },
              collapsed: true,
              items: [
                { label: 'File Uploads', slug: 'docs/guides/file-uploads', translations: { 'zh-CN': '文件上传' } },
                { label: 'WebSocket', slug: 'docs/guides/websocket', translations: { 'zh-CN': 'WebSocket' } },
                { label: 'Integrate AI', slug: 'docs/guides/integrate-ai', translations: { 'zh-CN': '集成 AI' } },
                { label: 'Multi-tenancy', slug: 'docs/guides/multi-tenancy', translations: { 'zh-CN': '多租户' } },
              ],
            },
            {
              label: 'Observe & Deploy',
              translations: { 'zh-CN': '可观测与部署' },
              collapsed: true,
              items: [
                { label: 'Health & Readiness', slug: 'docs/guides/health-and-readiness', translations: { 'zh-CN': '健康检查' } },
                { label: 'Structured Logging', slug: 'docs/guides/structured-logging', translations: { 'zh-CN': '结构化日志' } },
                { label: 'Observability', slug: 'docs/guides/observability-integration', translations: { 'zh-CN': '可观测性集成' } },
                { label: 'Graceful Shutdown', slug: 'docs/guides/graceful-shutdown', translations: { 'zh-CN': '优雅停机' } },
                { label: 'Deploy with Docker', slug: 'docs/guides/deploy-with-docker', translations: { 'zh-CN': 'Docker 部署' } },
                { label: 'Dev Server', slug: 'docs/guides/dev-server', translations: { 'zh-CN': '开发服务器' } },
              ],
            },
            {
              label: 'Migrate',
              translations: { 'zh-CN': '迁移' },
              collapsed: true,
              items: [
                { label: 'Adoption Path', slug: 'docs/guides/adoption-path', translations: { 'zh-CN': '采用路径' } },
                { label: 'Migration & Upgrades', slug: 'docs/guides/migration-and-upgrades', translations: { 'zh-CN': '迁移与升级' } },
                { label: 'Migrate from Gin/Echo', slug: 'docs/guides/migrate-from-gin-echo', translations: { 'zh-CN': '从 Gin/Echo 迁移' } },
                { label: 'Migrate from Chi', slug: 'docs/guides/migrate-from-chi', translations: { 'zh-CN': '从 Chi 迁移' } },
              ],
            },
          ],
        },
        {
          label: 'Reference',
          translations: { 'zh-CN': '参考' },
          items: [
            { label: 'API Overview', slug: 'docs/reference', translations: { 'zh-CN': 'API 总览' } },
            { label: 'core API', slug: 'docs/reference/api-core', translations: { 'zh-CN': 'core API' } },
            { label: 'contract API', slug: 'docs/reference/api-contract', translations: { 'zh-CN': 'contract API' } },
            { label: 'router API', slug: 'docs/reference/api-router', translations: { 'zh-CN': 'router API' } },
            { label: 'Error Reference', slug: 'docs/reference/errors', translations: { 'zh-CN': '错误参考' } },
            { label: 'Stability & Deprecation', slug: 'docs/reference/stability', translations: { 'zh-CN': '稳定性与弃用' } },
            { label: 'Release Posture', slug: 'docs/release-posture', translations: { 'zh-CN': '发布策略' } },
            { label: 'Style Guide', slug: 'docs/guides/style-guide', translations: { 'zh-CN': '编码规范' } },
          ],
        },
        {
          label: 'Advanced',
          translations: { 'zh-CN': '高级主题' },
          collapsed: true,
          items: [
            { label: 'Repo Control Plane', slug: 'docs/concepts/repo-control-plane', translations: { 'zh-CN': '仓库控制面' } },
            { label: 'Core Boundaries', slug: 'docs/concepts/core-boundaries', translations: { 'zh-CN': '核心边界' } },
            { label: 'Extension Boundaries', slug: 'docs/concepts/extension-boundaries', translations: { 'zh-CN': '扩展边界' } },
          ],
        },
      ],
    }),
    mdx(),
  ],
  markdown: {
    shikiConfig: {
      themes: {
        light: 'github-light',
        dark: 'github-dark',
      },
      wrap: true,
    },
  },
});
