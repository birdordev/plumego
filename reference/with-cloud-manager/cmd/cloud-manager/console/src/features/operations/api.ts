import { collectionReader } from '../../shared/collection/read-page'
import { request } from '../session/client'
import { operationPageSchema } from './schema'

export const readPage = collectionReader(request, '/operations', operationPageSchema, 'cursor')
