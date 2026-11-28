import { Fragment, type ReactNode } from 'react'

export type DetailField = readonly [label: string, value: ReactNode]

export function DetailFields({ fields }: { fields: readonly DetailField[] }) {
  return <dl className="detail-list">
    {fields.map(([label, value], index) => <Fragment key={label + '-' + index}>
      <dt>{label}</dt><dd>{value ?? '—'}</dd>
    </Fragment>)}
  </dl>
}
