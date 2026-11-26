import { isAbsolute, relative, sep } from 'node:path'
import { featureEntrypoints } from './feature-entrypoints.mjs'

const layers = {
  entry: new Set(['app']),
  app: new Set(['app', 'features', 'shared']),
  features: new Set(['features', 'shared']),
  shared: new Set(['shared']),
}

export function sourceArea(source, file) {
  const path = relative(source, file)
  if (path === '..' || path.startsWith('..' + sep) || isAbsolute(path)) return
  const [layer, ...parts] = path.split(sep)
  return parts.length ? { layer, parts } : { layer: 'entry', parts: [] }
}

export function canImport(from, to) {
  if (!layers[from.layer]?.has(to.layer)) return false
  if (from.layer !== 'features' || to.layer !== 'features') return true
  return from.parts[0] === to.parts[0] || featureEntrypoints.has(to.parts.join('/'))
}
