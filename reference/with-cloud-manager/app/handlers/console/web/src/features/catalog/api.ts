import { collectionReader } from '../../shared/collection/read-page'
import { request } from '../session/client'
import { catalogPageSchema } from './schema'

export const readPage = collectionReader(request, '/service-catalog', catalogPageSchema, 'after', (query, admin) => {
  if (admin) query.set('include_disabled', 'true')
})
