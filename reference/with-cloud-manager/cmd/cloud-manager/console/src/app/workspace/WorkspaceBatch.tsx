import { lazy, Suspense, type Ref } from 'react'
import type { BatchEditor } from '../../features/batches/BatchPage'
import { FeatureBoundary } from '../../shared/components/FeatureBoundary'

const BatchPage = lazy(() => import('../../features/batches/BatchPage'))

type Props = {
  base: string
  canExecute: boolean
  active: boolean
  initialInput: string
  editor: Ref<BatchEditor>
}

export function WorkspaceBatch({ base, canExecute, active, initialInput, editor }: Props) {
  return <div hidden={!active}>
    <FeatureBoundary message="无法加载批量操作，请刷新后重试。">
      <Suspense fallback={active ? <p role="status">正在加载批量操作…</p> : null}>
        <BatchPage ref={editor} base={base} canExecute={canExecute} initialInput={initialInput} />
      </Suspense>
    </FeatureBoundary>
  </div>
}
