import { clusterSnapshotReader } from '../snapshot'
import { nodePoolPageSchema } from './schema'

export const readNodePools = clusterSnapshotReader('node-pools', nodePoolPageSchema)
