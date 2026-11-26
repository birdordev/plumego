import { collectionReader } from '../../shared/collection/read-page'
import { request } from '../session/client'
import { grantPageSchema } from './schema'

export const readPage = collectionReader(request, '/console-grants', grantPageSchema, 'after')
