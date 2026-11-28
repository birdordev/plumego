import { pageSchema } from '../../../shared/api/page'
import { grantSchema } from '../schema'

export const grantPageSchema = pageSchema(grantSchema)
