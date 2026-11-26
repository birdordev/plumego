import type { ReactNode } from 'react'
import { useDetailAction } from './useDetailAction'

type Props<Result> = {
  children?: ReactNode
  stamp: number
  submitLabel: string
  onSubmit: (data: FormData) => Promise<Result>
  onSaved?: (result: Result) => void
  disabled?: boolean
}

export function DetailForm<Result>({ children, stamp, submitLabel, onSubmit, onSaved, disabled }: Props<Result>) {
  const { busy, run } = useDetailAction(stamp)
  return (
    <form className="catalog-form" aria-busy={busy} onSubmit={(event) => {
      event.preventDefault()
      if (disabled) return
      const data = new FormData(event.currentTarget)
      void run(() => onSubmit(data), onSaved)
    }}>
      <fieldset className="detail-form-fields" disabled={busy || disabled}>
        {children}
        <button type="submit" className="primary">{submitLabel}</button>
      </fieldset>
    </form>
  )
}
