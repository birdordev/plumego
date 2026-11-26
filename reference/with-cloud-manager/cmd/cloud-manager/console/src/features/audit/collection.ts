import { createCollection } from '../../shared/collection/store'
import { readPage } from './api'

export const auditCollection = createCollection(readPage)
