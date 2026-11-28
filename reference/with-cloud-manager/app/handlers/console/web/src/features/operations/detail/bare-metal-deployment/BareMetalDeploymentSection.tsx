import type { OperationDetail } from '../schema'
import { DeploymentRegistration } from './DeploymentRegistration'
import { DeploymentSummary } from './DeploymentSummary'

type Props = { operation: OperationDetail; project: string; stamp: number; reload: () => void }

export function BareMetalDeploymentSection({ operation, project, stamp, reload }: Props) {
  const view = operation.bare_metal_deployment
  if (!view) return null

  return <section aria-label="裸金属部署">
    <h3>裸金属部署</h3>
    {view.deployment ? <>
      <DeploymentSummary deployment={view.deployment} bootStatus={operation.status} />
      <button type="button" onClick={reload}>刷新部署状态</button>
    </> : <DeploymentRegistration project={project} stage={view.stage_operation} boot={operation.id}
      stamp={stamp} reload={reload} />}
  </section>
}
