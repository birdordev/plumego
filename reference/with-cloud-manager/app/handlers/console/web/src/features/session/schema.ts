import { z } from 'zod'

export const tenantSchema = z.string().trim().min(1).max(256)
const projectSchema = z.object({ id: z.string().min(1), role: z.enum(['viewer', 'operator', 'approver', 'admin']) })

export const sessionSchema = z.object({
  tenant: tenantSchema,
  principal: z.string(),
  projects: z.array(projectSchema),
  csrf: z.string().min(1),
  native_console: z.boolean().optional(),
  native_serial_console: z.boolean().optional(),
})

