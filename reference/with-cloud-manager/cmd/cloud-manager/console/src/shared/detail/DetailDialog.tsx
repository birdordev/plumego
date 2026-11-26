import { useLayoutEffect, useRef, useSyncExternalStore } from 'react'
import { FeatureBoundary } from '../components/FeatureBoundary'
import { closeDetail, detailSnapshot, subscribeDetail } from './store'

export function DetailDialog() {
  const detail = useSyncExternalStore(subscribeDetail, detailSnapshot)
  const dialog = useRef<HTMLDialogElement>(null)
  const close = useRef<HTMLButtonElement>(null)
  const stamp = detail?.stamp

  useLayoutEffect(() => {
    const node = dialog.current
    if (!node) return
    if (stamp === undefined) {
      node.close()
    } else {
      if (!node.open) node.showModal()
      close.current?.focus()
    }
  }, [stamp])

  return (
    <dialog ref={dialog} id="detail" aria-labelledby="detail-title"
      onCancel={(event) => { event.preventDefault(); closeDetail() }}>
      <header className="detail-header">
        <h2 id="detail-title">{detail?.title}</h2>
        <button ref={close} type="button" id="close-detail" onClick={closeDetail}>关闭</button>
      </header>
      <p id="detail-message" className="notice" role={detail?.notice?.error ? 'alert' : 'status'}
        data-error={detail?.notice?.error}>{detail?.notice?.text}</p>
      <div id="detail-body">
        {detail ? <FeatureBoundary key={stamp}>{detail.content}</FeatureBoundary> : null}
      </div>
    </dialog>
  )
}
