import { collectionReader } from '../../../shared/collection/read-page'
import { request } from '../../session/client'
import { idlePageSchema } from './schema'

export const readPage = collectionReader(request, '/governance/idle-instances', idlePageSchema, 'cursor')
