import type { ReactNode } from 'react'
import { DetailForm } from '../../../shared/detail/DetailForm'
import { showOperation } from '../detail/open'
import { requestVersionedOperation } from '../submission'

type Props = {
  stamp: number
  target: string
  revision?: number
  submitLabel: string
  payload: (data: FormData) => object
  children?: ReactNode
}

export function OperationForm({ stamp, target, revision, submitLabel, payload, children }: Props) {
  return <DetailForm stamp={stamp} submitLabel={submitLabel}
    onSubmit={async (data) => requestVersionedOperation(target, JSON.stringify(payload(data)), revision)}
    onSaved={(receipt) => showOperation(receipt.id)}>
    {children}
  </DetailForm>
}
