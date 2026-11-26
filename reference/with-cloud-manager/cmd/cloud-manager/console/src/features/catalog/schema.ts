import { z } from 'zod'
import { revision, time } from '../../shared/api/fields'
import { pageSchema } from '../../shared/api/page'

export const catalogVersionSchema = z.object({
  id: z.string(),
  version: revision,
  title: z.string(),
  account: z.string(),
  region: z.string(),
  size: z.string(),
  image_id: z.string(),
  network_id: z.string(),
  security_group_id: z.string(),
  published_at: time,
  published_by: z.string(),
})

export const catalogTemplateSchema = catalogVersionSchema.extend({
  revision,
  enabled: z.boolean(),
})

export type CatalogTemplate = z.infer<typeof catalogTemplateSchema>

export const catalogPageSchema = pageSchema(catalogTemplateSchema)
