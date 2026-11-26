import { z } from 'zod'
import { revision } from '../../../../shared/api/fields'

const renameIntent = z.object({
  product: z.string(),
  current_name: z.string(),
  name: z.string(),
  ownership_revision: revision,
}).optional()

export const renameIntents = {
  database_rename_intent: renameIntent,
  cache_rename_intent: renameIntent,
}
