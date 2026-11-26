import { relative, resolve, sep } from 'node:path'
import { fileURLToPath } from 'node:url'
import ts from 'typescript'
import { featureEntrypoints } from './feature-entrypoints.mjs'

const root = fileURLToPath(new URL('../', import.meta.url))
const source = resolve(root, 'src')
const config = resolve(root, 'tsconfig.json')
const project = ts.getParsedCommandLineOfConfigFile(config, {}, {
  ...ts.sys,
  onUnRecoverableConfigFileDiagnostic(error) {
    throw new Error(ts.flattenDiagnosticMessageText(error.messageText, '\n'))
  },
})

const layers = {
  entry: new Set(['app']),
  app: new Set(['app', 'features', 'shared']),
  features: new Set(['features', 'shared']),
  shared: new Set(['shared']),
}
const resolutionCache = ts.createModuleResolutionCache(root, (path) => path, project.options)
let violations = 0

function sourceArea(file) {
  const path = relative(source, file)
  if (path.startsWith('..' + sep)) return
  const [layer, ...parts] = path.split(sep)
  return parts.length ? { layer, parts } : { layer: 'entry', parts: [] }
}

function canImport(from, to) {
  if (!layers[from.layer]?.has(to.layer)) return false
  if (from.layer !== 'features' || to.layer !== 'features') return true
  return from.parts[0] === to.parts[0] || featureEntrypoints.has(to.parts.join('/'))
}

for (const file of project.fileNames) {
  const from = sourceArea(file)
  if (!from) continue
  const { importedFiles } = ts.preProcessFile(ts.sys.readFile(file), true, true)
  for (const { fileName } of importedFiles) {
    const { resolvedModule } = ts.resolveModuleName(fileName, file, project.options, ts.sys, resolutionCache)
    if (!resolvedModule) continue
    const target = sourceArea(resolvedModule.resolvedFileName)
    if (!target || canImport(from, target)) continue
    console.error(`${relative(root, file)}: import "${fileName}" is outside the declared module boundary`)
    violations++
  }
}

if (violations) process.exitCode = 1
