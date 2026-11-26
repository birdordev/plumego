import { clusterSnapshotReader } from '../snapshot'
import { workloadPageSchema } from './schema'

export const readWorkloads = clusterSnapshotReader('workloads', workloadPageSchema)
