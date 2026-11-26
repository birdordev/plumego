import { createCollection } from '../../../shared/collection/store'

import { readPage } from './api'

export const idleCollection = createCollection(readPage)
