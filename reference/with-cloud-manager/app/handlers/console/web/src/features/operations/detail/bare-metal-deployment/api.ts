import { request } from '../../../session/client'
import { deploymentSchema, type Registration } from './schema'

export function registerDeployment(project: string, input: Registration) {
  return request(project + '/bare-metal/deployments', { method: 'POST', body: JSON.stringify(input) }, deploymentSchema)
}
