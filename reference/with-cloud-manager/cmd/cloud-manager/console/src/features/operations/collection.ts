import { createCollection } from '../../shared/collection/store'

import { readPage } from './api'

export const operationsCollection = createCollection(readPage)
