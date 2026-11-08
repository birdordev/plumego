import fs from 'node:fs/promises';
import path from 'node:path';
import { REPO_ROOT, WEBSITE_ROOT } from './_shared.mjs';

const DOCS_ROOT = path.join(WEBSITE_ROOT, 'src', 'content', 'docs');
const PAGES_ROOT = path.join(WEBSITE_ROOT, 'src', 'pages');
const SITE_DATA_FILE = path.join(WEBSITE_ROOT, 'src', 'data', 'site.ts');
const MODULE_IMPORT_PREFIX = 'github.com/birdordev/plumego/';
const repoPathPrefixes = [
  'AGENTS.md',
  'CLAUDE.md',
  'README.md',
  'README_CN.md',
  'cmd',
  'contract',
  'core',
  'docs',
  'env.example',
  'health',
  'internal',
  'log',
  'metrics',
  'middleware',
  'reference',
  'router',
  'security',
  'specs',
  'store',
  'tasks',
  'x',
];

const packageDirs = {
  contract: path.join(REPO_ROOT, 'contract'),
  core: path.join(REPO_ROOT, 'core'),
  httpmetrics: path.join(REPO_ROOT, 'middleware', 'httpmetrics'),
  jwt: path.join(REPO_ROOT, 'security', 'jwt'),
  log: path.join(REPO_ROOT, 'log'),
  metrics: path.join(REPO_ROOT, 'metrics'),
  middleware: path.join(REPO_ROOT, 'middleware'),
  observability: path.join(REPO_ROOT, 'x', 'observability'),
  rest: path.join(REPO_ROOT, 'x', 'rest'),
  resolve: path.join(REPO_ROOT, 'x', 'tenant', 'resolve'),
  router: path.join(REPO_ROOT, 'router'),
  websocket: path.join(REPO_ROOT, 'x', 'websocket'),
};

const broadPackageChecks = new Set(['contract', 'metrics', 'resolve', 'rest', 'websocket']);

const bannedPatterns = [
  {
    packageName: 'rest',
    pattern: /\brest\.NewQueryBuilder\s*\(\s*r\s*\)/,
    message: 'use rest.NewQueryBuilder().WithPageSize(...).Parse(r)',
  },
  {
    packageName: 'rest',
    pattern: /\bWithMaxPageSize\s*\(/,
    message: 'WithMaxPageSize is not part of the current x/rest QueryBuilder API',
  },
  {
    packageName: 'rest',
    pattern: /\bWithDefaultPageSize\s*\(/,
    message: 'WithDefaultPageSize is not part of the current x/rest QueryBuilder API',
  },
  {
    packageName: 'rest',
    pattern: /filter\[[^\]]+\]/,
    message: 'x/rest QueryBuilder treats non-reserved query keys as filters; do not document filter[field] syntax',
  },
  {
    packageName: 'httpmetrics',
    pattern: /\bhttpmetrics\.New\s*\(/,
    message: 'use httpmetrics.Middleware(...)',
  },
  {
    packageName: 'log',
    pattern: /\blog\.With(?:Level|Output)\s*\(/,
    message: 'use log.NewLogger(log.LoggerConfig{...})',
  },
  {
    packageName: 'log',
    pattern: /\b(?:log|plumelog)\.NewLoggerWithConfig\s*\(/,
    message: 'use NewLogger(LoggerConfig{...})',
  },
  {
    packageName: 'log',
    pattern: /\blog\.Level(?:Debug|Info|Warn|Error|Fatal)\b/,
    message: 'use log.DEBUG, log.INFO, log.WARNING, log.ERROR, or log.FATAL',
  },
  {
    packageName: 'db',
    pattern: /\bstore\/db\.Open\b/,
    message: 'import github.com/birdordev/plumego/store/db and call db.Open',
  },
  {
    packageName: 'cache',
    pattern: /\bstore\/cache\.MemoryCache\b/,
    message: 'import github.com/birdordev/plumego/store/cache and refer to cache.MemoryCache',
  },
  {
    packageName: 'store',
    pattern: /\bstore\.(?:DB|Cache)\b/,
    message: 'there is no root store.DB or store.Cache; use db.DB or cache.Cache from the store subpackages',
  },
  {
    packageName: 'websocket',
    pattern: /\bhub\.Broadcast\b/,
    message: 'use hub.BroadcastRoom(...) or hub.BroadcastAll(...)',
  },
  {
    packageName: 'websocket',
    pattern: /\bwebsocket\.NewHub\s*\(/,
    message: 'use websocket.NewHubWithConfig(...) or websocket.New(...)',
  },
  {
    packageName: 'middleware',
    pattern: /\bmiddleware\.Auth\s*\(/,
    message: 'use middleware/auth.Authenticate(...) or a documented subpackage middleware',
  },
  {
    packageName: 'tenant',
    pattern: /\btenant\.(?:FromJWT|Guard)\s*\(/,
    message: 'use x/tenant/resolve, policy, quota, and ratelimit middleware entrypoints',
  },
];

const codeBlockBannedPatterns = [
  {
    packageName: 'docs',
    pattern: /[“”]/,
    message: 'use ASCII quotes in Go code blocks',
  },
];

async function listFiles(dir, predicate) {
  const entries = await fs.readdir(dir, { withFileTypes: true });
  const files = await Promise.all(
    entries.map(async (entry) => {
      const fullPath = path.join(dir, entry.name);
      if (entry.isDirectory()) {
        return listFiles(fullPath, predicate);
      }
      if (entry.isFile() && predicate(fullPath)) {
        return [fullPath];
      }
      return [];
    }),
  );
  return files.flat();
}

async function packageSymbols(packageDir) {
  const entries = await fs.readdir(packageDir, { withFileTypes: true });
  const files = entries
    .filter((entry) => entry.isFile() && entry.name.endsWith('.go') && !entry.name.endsWith('_test.go'))
    .map((entry) => path.join(packageDir, entry.name));
  const symbols = new Set();

  for (const file of files) {
    const source = await fs.readFile(file, 'utf8');
    for (const match of source.matchAll(/^(?:func|type|const|var)\s+(?:\([^)]*\)\s*)?([A-Z][A-Za-z0-9_]*)\b/gm)) {
      symbols.add(match[1]);
    }
    for (const block of source.matchAll(/^const\s*\(\n([\s\S]*?)^\)/gm)) {
      for (const match of block[1].matchAll(/^\s*([A-Z][A-Za-z0-9_]*)\b/gm)) {
        symbols.add(match[1]);
      }
    }
    for (const block of source.matchAll(/^var\s*\(\n([\s\S]*?)^\)/gm)) {
      for (const match of block[1].matchAll(/^\s*([A-Z][A-Za-z0-9_]*)\b/gm)) {
        symbols.add(match[1]);
      }
    }
  }

  return symbols;
}

function packageDirForImportPath(importPath) {
  if (!importPath.startsWith(MODULE_IMPORT_PREFIX)) {
    return undefined;
  }
  const relImport = importPath.slice(MODULE_IMPORT_PREFIX.length);
  if (relImport.includes('..')) {
    return undefined;
  }
  return path.join(REPO_ROOT, ...relImport.split('/'));
}

const importSymbolCache = new Map();

async function symbolsForImportPath(importPath) {
  if (importSymbolCache.has(importPath)) {
    return importSymbolCache.get(importPath);
  }

  const packageDir = packageDirForImportPath(importPath);
  if (!packageDir) {
    importSymbolCache.set(importPath, undefined);
    return undefined;
  }

  try {
    const symbols = await packageSymbols(packageDir);
    importSymbolCache.set(importPath, symbols);
    return symbols;
  } catch (err) {
    if (err?.code !== 'ENOENT') {
      throw err;
    }
    importSymbolCache.set(importPath, undefined);
    return undefined;
  }
}

function lineNumber(text, index) {
  return text.slice(0, index).split('\n').length;
}

function goCodeBlocks(text) {
  return [...text.matchAll(/```go\n([\s\S]*?)```/g)].map((match) => ({
    code: match[1],
    index: match.index,
  }));
}

function importAliases(code) {
  const aliases = new Map();
  const addImport = (alias, importPath) => {
    if (!importPath.startsWith(MODULE_IMPORT_PREFIX)) return;
    if (alias === '_' || alias === '.') return;
    const inferred = importPath.split('/').at(-1);
    aliases.set(alias || inferred, importPath);
  };

  for (const block of code.matchAll(/import\s*\(([\s\S]*?)\)/g)) {
    for (const line of block[1].split('\n')) {
      const match = line.trim().match(/^(?:(\w+)\s+)?"([^"]+)"/);
      if (match) {
        addImport(match[1], match[2]);
      }
    }
  }

  for (const match of code.matchAll(/import\s+(?:(\w+)\s+)?"([^"]+)"/g)) {
    addImport(match[1], match[2]);
  }

  return aliases;
}

function looksLikeRepoPath(value) {
  if (value.includes(' ') || value.includes('*') || value.includes('|') || value.includes(':')) {
    return false;
  }
  if (!repoPathPrefixes.some((prefix) => value === prefix || value.startsWith(`${prefix}/`))) {
    return false;
  }
  if (!value.includes('/') && !value.endsWith('.md') && !value.endsWith('.yaml') && !value.endsWith('.example')) {
    return false;
  }

  const basename = value.split('/').at(-1).replace(/\/$/, '');
  const ext = path.extname(basename);
  return ext === '' || ['.go', '.md', '.yaml', '.yml', '.json', '.example'].includes(ext);
}

async function repoPathExists(value) {
  const target = path.join(REPO_ROOT, value.replace(/\/$/, ''));
  try {
    await fs.access(target);
    return true;
  } catch (err) {
    if (err?.code !== 'ENOENT') {
      throw err;
    }
    return false;
  }
}

const [docsFiles, pageFiles, symbolMaps] = await Promise.all([
  listFiles(DOCS_ROOT, (file) => file.endsWith('.mdx')),
  listFiles(PAGES_ROOT, (file) => file.endsWith('.astro')),
  Promise.all(
    Object.entries(packageDirs).map(async ([name, dir]) => [name, await packageSymbols(dir)]),
  ).then((entries) => Object.fromEntries(entries)),
]);

const checkedFiles = [...docsFiles, ...pageFiles, SITE_DATA_FILE];
const failures = [];

for (const file of checkedFiles) {
  const text = await fs.readFile(file, 'utf8');
  const rel = path.relative(REPO_ROOT, file);

  for (const [packageName, symbols] of Object.entries(symbolMaps)) {
    if (!broadPackageChecks.has(packageName)) continue;
    const pattern = new RegExp(`\\b${packageName}\\.([A-Z][A-Za-z0-9_]*)\\b`, 'g');
    for (const match of text.matchAll(pattern)) {
      const symbol = match[1];
      if (!symbols.has(symbol)) {
        failures.push(`${rel}:${lineNumber(text, match.index)} unknown ${packageName}.${symbol}`);
      }
    }
  }

  for (const block of goCodeBlocks(text)) {
    for (const banned of codeBlockBannedPatterns) {
      const match = banned.pattern.exec(block.code);
      if (match) {
        failures.push(`${rel}:${lineNumber(text, block.index + match.index)} stale ${banned.packageName} docs: ${banned.message}`);
      }
    }

    for (const [alias, importPath] of importAliases(block.code)) {
      const symbols = await symbolsForImportPath(importPath);
      if (!symbols) continue;
      const pattern = new RegExp(`\\b${alias}\\.([A-Z][A-Za-z0-9_]*)\\b`, 'g');
      for (const match of block.code.matchAll(pattern)) {
        const symbol = match[1];
        if (!symbols.has(symbol)) {
          failures.push(`${rel}:${lineNumber(text, block.index + match.index)} unknown ${alias}.${symbol}`);
        }
      }
    }
  }

  for (const banned of bannedPatterns) {
    const match = banned.pattern.exec(text);
    if (match) {
      failures.push(`${rel}:${lineNumber(text, match.index)} stale ${banned.packageName} docs: ${banned.message}`);
    }
  }

  for (const match of text.matchAll(/`([^`]+)`/g)) {
    const value = match[1];
    if (looksLikeRepoPath(value) && !(await repoPathExists(value))) {
      failures.push(`${rel}:${lineNumber(text, match.index)} missing repo path ${value}`);
    }
  }
}

if (failures.length > 0) {
  console.error('Docs API symbol check failed:');
  for (const failure of failures) {
    console.error(`- ${failure}`);
  }
  process.exit(1);
}

console.log(`Docs API symbol check passed (${checkedFiles.length} website content files).`);
