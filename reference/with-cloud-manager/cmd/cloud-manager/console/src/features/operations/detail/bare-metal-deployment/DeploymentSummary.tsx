import { DetailFields } from '../../../../shared/components/DetailFields'
import { timestamp } from '../../../../shared/display/time'
import type { Deployment } from './schema'

const states = {
  pending: '等待开机和安装回报',
  installed: '安装器已报告完成',
  failed: '安装器已报告失败',
  boot_failed: '开机失败',
  expired: '回报期限已过',
} satisfies Record<Deployment['state'], string>

export function DeploymentSummary({ deployment, bootStatus }: { deployment: Deployment; bootStatus: string }) {
  const state = deployment.state === 'pending' && bootStatus === 'succeeded' ? '等待安装器回报' : states[deployment.state]
  return <DetailFields fields={[
    ['部署 ID', deployment.id],
    ['部署状态', state],
    ['物理系统 UUID', deployment.system_id],
    ['一次性 PXE 操作', deployment.stage_operation],
    ['安装器服务主体', deployment.installer_id],
    ['安装镜像 SHA-256', deployment.image_sha256],
    ['回报截止时间', timestamp(deployment.expires_at)],
    ...(deployment.recorded_at ? [
      ['安装证据 SHA-256', deployment.evidence_sha256],
      ['收到回报时间', timestamp(deployment.recorded_at)],
    ] as const : []),
  ]} />
}
