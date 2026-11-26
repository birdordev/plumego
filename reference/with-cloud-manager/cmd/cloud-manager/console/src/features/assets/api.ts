import { collectionReader } from '../../shared/collection/read-page'
import { request } from '../session/client'
import { assetPageSchema } from './schema'

export const readPage = collectionReader(request, '/assets', assetPageSchema, 'cursor')
