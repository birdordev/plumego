import { collectionReader } from '../../shared/collection/read-page'
import { request } from '../session/client'
import { alertPageSchema } from './schema'

export const readPage = collectionReader(request, '/alerts', alertPageSchema, 'after')
