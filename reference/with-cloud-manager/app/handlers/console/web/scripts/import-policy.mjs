import { isAbsolute, relative, sep } from 'node:path'
import { appFeatureEntrypoints, featureEntrypoints } from './feature-entrypoints.mjs'

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

export function canImport(from, to, kind) {
  if (!layers[from.layer]?.has(to.layer)) return false
  if (to.layer !== 'features') return true

  const target = to.parts.join('/')
  if (from.layer === 'features') {
    return from.parts[0] === to.parts[0] || featureEntrypoints.has(target)
  }
  if (featureEntrypoints.has(target) || appFeatureEntrypoints.has(target)) return true
  if (from.parts[0] === 'routes' && kind === 'dynamic') {
    const file = to.parts.at(-1)
    return file.endsWith('Page.tsx') || file === 'collection.ts'
  }
  return from.parts[0] === 'workspace' && target === 'batches/BatchPage.tsx' && (kind === 'dynamic' || kind === 'type')
}
