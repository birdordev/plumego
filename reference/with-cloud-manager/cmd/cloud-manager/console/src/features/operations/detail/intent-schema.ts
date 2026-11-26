import { z } from 'zod'
import { dnsRecordSchema } from '../../assets/dns/schema'

const text = z.string()
const integer = z.number().int()
const flag = z.boolean()
const ownership = { ownership_revision: integer }
const group = { target_group_id: text, ...ownership }
const member = { ...group, member_key: text }

export const operationIntents = {
  workload_scale_intent: z.object({
    uid: text, namespace: text, kind: text, name: text, current_replicas: integer, replicas: integer,
  }).optional(),
  dns_record_creation_intent: z.object({
    zone_name: text, name: text, type: text, value: text, ttl: integer, record_id: text.optional(), needs_record_id: flag.optional(),
  }).optional(),
  dns_record_deletion_intent: z.object({ zone_name: text, record: dnsRecordSchema }).optional(),
  dns_record_status_intent: z.object({ zone_name: text, name: text, record_id: text, current_status: text, enabled: flag }).optional(),
  image_copy_intent: z.object({
    account: text, region: text, asset_id: text, source_revision: integer, destination_account: text,
    destination_region: text, name: text, can_retry_observation: flag.optional(),
  }).optional(),
  image_creation_intent: z.object({ name: text, instance_id: text, instance_revision: integer }).optional(),
  image_deletion_intent: z.object({ name: text, ...ownership }).optional(),
  load_balancer_backend_attach_intent: z.object({
    ...group, instance_id: text, instance_revision: integer, port: integer, member_key: text.optional(),
  }).optional(),
  load_balancer_backend_detach_intent: z.object(member).optional(),
  load_balancer_backend_weight_intent: z.object({ ...member, current_weight: integer, weight: integer }).optional(),
  load_balancer_rename_intent: z.object({
    ...ownership, product: text, current_name: text, name: text, listener_id: text.optional(), target_group_id: text.optional(),
  }).optional(),
  snapshot_intent: z.object({
    name: text, volume_id: text, volume_revision: integer, allow_attached: flag.optional(),
    class: text.optional(), can_retry_observation: flag.optional(),
  }).optional(),
  snapshot_restore_intent: z.object({
    snapshot_id: text, snapshot_class: text, source_volume_id: text, source_volume_revision: integer,
    name: text, zone: text, disk_type: text, size_gib: integer, encrypted: flag,
  }).optional(),
  network_deletion_intent: z.object({
    ...ownership, name: text, parent_id: text.optional(), parent_revision: integer.optional(), rule_removal_confirmed: flag.optional(),
  }).optional(),
  network_intent: z.object({
    name: text, parent_revision: integer, parent_id: text.optional(), cidr: text.optional(), zone: text.optional(),
    bandwidth_mbps: integer.optional(), address_pool: text.optional(), asset_revision: integer.optional(),
    public_address: text.optional(), port_id: text.optional(),
    rule: z.object({ direction: text, protocol: text, from_port: integer, to_port: integer, remote_cidr: text }).optional(),
  }).optional(),
}
