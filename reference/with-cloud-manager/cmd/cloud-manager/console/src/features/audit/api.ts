import { collectionReader } from '../../shared/collection/read-page'
import { request } from '../session/client'
import { auditPageSchema } from './schema'

export const readPage = collectionReader(request, '/audit', auditPageSchema, 'cursor')
