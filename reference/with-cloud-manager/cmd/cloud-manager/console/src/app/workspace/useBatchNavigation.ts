import { useRef, useState } from 'react'
import type { BatchEditor } from '../../features/batches/BatchPage'
import { formatSelectedInstances } from '../../features/batches/request/selection-format'
import type { View } from '../routes/registry'

export function useBatchNavigation(view: View, selectView: (view: View) => void) {
  const editor = useRef<BatchEditor>(null)
  const [visited, setVisited] = useState(false)
  const [initialInput, setInitialInput] = useState('')

  if (view === 'batch' && !visited) setVisited(true)

  function openSelectedBatch() {
    if (editor.current) editor.current.fill()
    else setInitialInput(formatSelectedInstances('start'))
    selectView('batch')
  }

  return { editor, visited, initialInput, openSelectedBatch }
}
