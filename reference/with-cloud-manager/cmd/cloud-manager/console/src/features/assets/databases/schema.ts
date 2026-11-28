import { z } from 'zod'
import { time } from '../../../shared/api/fields'

export const databaseSchema = z.object({
  product: z.string().min(1),
  engine: z.string().min(1),
  version: z.string().optional(),
})

const backupSchema = z.object({
  id: z.string().min(1),
  kind: z.enum(['full', 'incremental', 'fragment', 'unknown']),
  mode: z.enum(['automatic', 'manual', 'unknown']),
  status: z.enum(['ready', 'pending', 'failed', 'deleting', 'unknown']),
  started_at: time,
  ended_at: time.optional(),
  size_bytes: z.string().regex(/^\d+$/).optional(),
})

export const databaseBackupPageSchema = z.object({
  observed_at: time,
  window_start: time,
  window_end: time,
  backups: z.array(backupSchema).max(100),
  total: z.number().int().nonnegative(),
  next_offset: z.number().int().positive().optional(),
})

export type DatabaseBackupPage = z.infer<typeof databaseBackupPageSchema>

export const databaseRestoreWindowsSchema = z.object({
  observed_at: time,
  scope: z.enum(['instance', 'source_instance']),
  windows: z.array(z.object({ start_at: time, end_at: time })).max(512),
})
