import type { OperationDetail } from '../schema'

const observationActions = new Set([
  'power-on-bare-metal', 'shutdown-bare-metal', 'stage-bare-metal-pxe', 'cancel-bare-metal-pxe',
  'revoke-security-group-rule', 'release-elastic-ip', 'bind-elastic-ip', 'unbind-elastic-ip',
  'delete-snapshot', 'delete-image', 'delete-network', 'delete-subnet', 'delete-security-group',
  'restore-snapshot', 'resize-instance', 'set-bucket-versioning', 'resize-volume', 'release-volume', 'rename-load-balancer', 'rename-load-balancer-listener', 'rename-load-balancer-target-group',
  'set-load-balancer-backend-weight', 'detach-load-balancer-backend', 'attach-load-balancer-backend',
  'set-dns-record-status', 'delete-dns-record', 'scale-workload', 'rename-database-instance', 'rename-cache-instance',
])

export function reviewChoices(op: OperationDetail) {
  const observationOnly = op.action.startsWith('create') || observationActions.has(op.action)
  const observe = observationOnly || Boolean(op.can_retry_power_observation) ||
    (op.action === 'copy-image' && op.image_copy_intent?.can_retry_observation) ||
    (op.action === 'rollback-snapshot' && op.snapshot_intent?.can_retry_observation) ||
    (op.action === 'retire-instance' && Boolean(op.provider_request_id))
  const succeed = !observationOnly && op.action !== 'copy-image'
  return { observe, succeed, needsRecordID: Boolean(op.dns_record_creation_intent?.needs_record_id) }
}
