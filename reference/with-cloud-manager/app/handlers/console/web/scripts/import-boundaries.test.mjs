import assert from 'node:assert/strict'
import { resolve } from 'node:path'
import { test } from 'node:test'
import ts from 'typescript'
import { firstImportCycle } from './import-cycles.mjs'
import { canImport, sourceArea } from './import-policy.mjs'
import { moduleReferences } from './module-references.mjs'

test('区分静态运行时导入、类型引用和延迟加载', () => {
  const source = ts.createSourceFile('example.tsx', [
    "import { Component, type Props } from './component'",
    "import { type Model } from './model'",
    "export type { Input } from './input'",
    "type Raw = import('./raw').Raw",
    "export { format } from './format'",
    "const load = () => import(`./page`)",
    "import type { Route } from './route'",
    "export { type Receipt } from './receipt'",
  ].join('\n'), ts.ScriptTarget.Latest, true)

  assert.deepEqual(moduleReferences(source), [
    { name: './component', line: 1, kind: 'static' },
    { name: './model', line: 2, kind: 'static' },
    { name: './input', line: 3, kind: 'type' },
    { name: './raw', line: 4, kind: 'type' },
    { name: './format', line: 5, kind: 'static' },
    { name: './page', line: 6, kind: 'dynamic' },
    { name: './route', line: 7, kind: 'type' },
    { name: './receipt', line: 8, kind: 'static' },
  ])
})

test('识别无法逐条验证的动态路径', () => {
  const source = ts.createSourceFile('example.ts', [
    "const load = (name: string) => import('./pages/' + name)",
    "const modules = import.meta.glob('./pages/*.tsx')",
  ].join('\n'), ts.ScriptTarget.Latest, true)

  assert.deepEqual(moduleReferences(source), [
    { name: undefined, line: 1, kind: 'dynamic' },
    { name: undefined, line: 2, kind: 'dynamic' },
  ])
})

test('多处复用模块不会当作循环，真实循环按导入顺序定位', () => {
  const graph = new Map([
    ['main', ['page', 'api']],
    ['page', ['api']],
    ['api', ['shared']],
  ])

  assert.equal(firstImportCycle(graph), undefined)
  graph.set('shared', ['page'])
  assert.deepEqual(firstImportCycle(graph), ['page', 'api', 'shared', 'page'])
})

const sourceRoot = resolve('src')
const area = (path) => sourceArea(sourceRoot, resolve(sourceRoot, path))
const allowed = (from, to, kind = 'static') => canImport(area(from), area(to), kind)

test('应用层可装配业务，共用层无法反向导入', () => {
  assert.equal(allowed('main.tsx', 'app/App.tsx'), true)
  assert.equal(allowed('main.tsx', 'features/assets/inventory/AssetsPage.tsx'), false)
  assert.equal(allowed('app/App.tsx', 'features/session/login/LoginPage.tsx'), true)
  assert.equal(allowed('app/App.tsx', 'features/assets/inventory/AssetsPage.tsx', 'dynamic'), false)
  assert.equal(allowed('features/assets/inventory/AssetsPage.tsx', 'shared/components/FeatureBoundary.tsx'), true)
  assert.equal(allowed('features/assets/inventory/AssetsPage.tsx', 'app/App.tsx'), false)
  assert.equal(allowed('shared/components/Thing.tsx', 'features/session/client.ts'), false)
  assert.equal(area('../src-legacy/main.tsx'), undefined)
})

test('路由延迟加载页面或列表，工作区通过登记的入口装配', () => {
  const route = 'app/routes/resources.ts'
  assert.equal(allowed(route, 'features/assets/inventory/AssetsPage.tsx', 'dynamic'), true)
  assert.equal(allowed(route, 'features/assets/inventory/AssetsPage.tsx'), false)
  assert.equal(allowed(route, 'features/assets/inventory/AssetsPage.tsx', 'type'), false)
  assert.equal(allowed(route, 'features/assets/inventory/collection.ts', 'dynamic'), true)
  assert.equal(allowed(route, 'features/assets/inventory/collection.ts'), false)
  assert.equal(allowed(route, 'features/assets/inventory/collection.ts', 'type'), false)
  assert.equal(allowed(route, 'features/assets/inventory/api.ts', 'dynamic'), false)
  const workspace = 'app/workspace/WorkspaceBatch.tsx'
  assert.equal(allowed(workspace, 'features/batches/BatchPage.tsx', 'dynamic'), true)
  assert.equal(allowed(workspace, 'features/batches/BatchPage.tsx', 'type'), true)
  assert.equal(allowed(workspace, 'features/batches/BatchPage.tsx'), false)
  assert.equal(allowed(workspace, 'features/batches/request/selection-format.ts'), true)
  assert.equal(allowed(workspace, 'features/batches/receipt/api.ts', 'dynamic'), false)
})

test('业务域内可以共用实现，跨业务域只能导入已登记的入口', () => {
  const assets = 'features/assets/inventory/AssetsPage.tsx'
  assert.equal(allowed(assets, 'features/assets/instances/row-actions.ts'), true)
  assert.equal(allowed(assets, 'features/operations/forms/OperationForm.tsx'), true)
  assert.equal(allowed(assets, 'features/operations/detail/OperationPanel.tsx'), false)
  assert.equal(allowed(assets, 'features/operations/detail/review/schema.ts', 'type'), false)
  assert.equal(allowed(assets, 'features/catalog/launch/attempts.ts'), false)
})
