import { DetailFields } from '../../../shared/components/DetailFields'
import type { Member } from './schema'

export function MemberFields({ member }: { member: Member }) {
  return <DetailFields fields={[
    ['虚拟机', member.vm_id], ['虚拟机版本', member.vm_revision], ['备份镜像', member.image_id],
    ['证据 SHA-256', member.evidence_sha256], ['要求有效备份回执', member.require_verified_backup ? '是' : '否'],
  ]} />
}
