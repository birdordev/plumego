import { useRef, useState } from 'react'
import type { BatchEditor } from '../../features/batches/BatchPage'
import { formatSelectedInstances } from '../../features/batches/request/selection-format'
import type { View } from '../routes/registry'

export function useBatchNavigation(selectView: (view: View) => void) {
  const editor = useRef<BatchEditor>(null)
  const [visited, setVisited] = useState(false)
  const [initialInput, setInitialInput] = useState('')

  function navigate(next: View) {
    if (next === 'batch') setVisited(true)
    selectView(next)
  }

  function openSelectedBatch() {
    if (editor.current) editor.current.fill()
    else setInitialInput(formatSelectedInstances('start'))
    navigate('batch')
  }

  return { editor, visited, initialInput, navigate, openSelectedBatch }
}
