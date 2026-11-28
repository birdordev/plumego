import { createCollection } from '../../shared/collection/store'
import { readInventory } from './api'

export const kvmCollection = createCollection(readInventory)
