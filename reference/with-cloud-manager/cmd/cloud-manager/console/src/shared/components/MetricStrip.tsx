import type { ReactNode } from 'react'

export type Metric = readonly [label: string, value: ReactNode]

export function MetricStrip({ values }: { values: readonly Metric[] }) {
  return <dl className="metric-strip">
    {values.map(([label, value]) => <div key={label} className="metric">
      <dt>{label}</dt><dd>{value}</dd>
    </div>)}
  </dl>
}
