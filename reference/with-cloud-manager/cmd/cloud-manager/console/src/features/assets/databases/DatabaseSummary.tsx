import type { z } from 'zod'
import type { databaseSchema } from './schema'

const engineNames: Record<string, string> = {
  mysql: 'MySQL',
  postgresql: 'PostgreSQL',
  sqlserver: 'SQL Server',
  mariadb: 'MariaDB',
}

export function DatabaseSummary({ database }: { database: z.infer<typeof databaseSchema> }) {
  const engine = engineNames[database.engine] ?? database.engine
  return <span className="minor">{[engine, database.version].filter(Boolean).join(' ')}</span>
}
