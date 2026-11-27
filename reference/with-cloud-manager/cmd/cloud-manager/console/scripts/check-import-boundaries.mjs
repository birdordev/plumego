import { relative, resolve } from 'node:path'
import { fileURLToPath } from 'node:url'
import ts from 'typescript'
import { appFeatureEntrypoints, featureEntrypoints } from './feature-entrypoints.mjs'
import { firstImportCycle } from './import-cycles.mjs'
import { canImport, sourceArea } from './import-policy.mjs'
import { moduleReferences } from './module-references.mjs'

const root = fileURLToPath(new URL('../', import.meta.url))
const source = resolve(root, 'src')
const config = resolve(root, 'tsconfig.json')
const project = ts.getParsedCommandLineOfConfigFile(config, {}, {
  ...ts.sys,
  onUnRecoverableConfigFileDiagnostic(error) {
    throw new Error(ts.flattenDiagnosticMessageText(error.messageText, '\n'))
  },
})

const resolutionCache = ts.createModuleResolutionCache(root, (path) => path, project.options)
const runtimeImports = new Map()
let violations = 0

function report(message) {
  console.error(message)
  violations++
}

for (const entry of [...featureEntrypoints, ...appFeatureEntrypoints]) {
  if (!ts.sys.fileExists(resolve(source, 'features', entry))) report(`模块入口不存在：${entry}`)
}

for (const file of project.fileNames) {
  const from = sourceArea(source, file)
  if (!from) continue
  const parsed = ts.createSourceFile(file, ts.sys.readFile(file), ts.ScriptTarget.Latest, true)
  const dependencies = []
  runtimeImports.set(file, dependencies)
  for (const { name, line, kind } of moduleReferences(parsed)) {
    const location = `${relative(root, file)}:${line}`
    if (!name) {
      report(`${location} 模块路径必须为显式的字符串字面量`)
      continue
    }

    const { resolvedModule } = ts.resolveModuleName(name, file, project.options, ts.sys, resolutionCache)
    if (!resolvedModule) continue
    const dependency = resolvedModule.resolvedFileName
    const target = sourceArea(source, dependency)
    if (!target) continue
    if (!canImport(from, target, kind)) report(`${location} 导入 "${name}" 超出模块边界`)
    if (kind === 'static') dependencies.push(dependency)
  }
}

const cycle = firstImportCycle(runtimeImports)
if (cycle) report(`检测到静态运行时循环依赖：${cycle.map((file) => relative(root, file)).join(' -> ')}`)
if (violations) process.exitCode = 1
