import type { ReactNode } from 'react'

export type TableRow = { key: string; cells: ReactNode[] }

export function ConsoleTable({ columns, rows }: { columns: string[]; rows: TableRow[] }) {
  return (
    <div className="table-scroll" tabIndex={0} aria-label="结果列表">
      <table>
        <thead><tr>{columns.map((label) => <th key={label} scope="col">{label}</th>)}</tr></thead>
        <tbody>{rows.map(({ key, cells }) => (
          <tr key={key}>{cells.map((value, cell) => <td key={cell}>{value ?? '—'}</td>)}</tr>
        ))}</tbody>
      </table>
    </div>
  )
}
