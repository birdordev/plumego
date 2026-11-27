import { pageSchema } from '../../../shared/api/page'
import { operationSchema } from '../schema'

export const operationPageSchema = pageSchema(operationSchema)
