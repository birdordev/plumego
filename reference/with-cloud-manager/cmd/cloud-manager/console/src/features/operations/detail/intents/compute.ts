import { z } from 'zod'

const text = z.string()
const integer = z.number().int()
const flag = z.boolean()

export const computeIntents = {
  volume_creation_intent: z.object({
    name: text, zone: text, disk_type: text, size_gib: integer.positive(), encrypted: flag,
  }).optional(),
  volume_release_intent: z.object({ name: text, size_gib: integer.positive(), ownership_revision: integer.positive() }).optional(),
  volume_resize_intent: z.object({
    current_gib: integer.positive(), target_gib: integer.positive(), ownership_revision: integer.positive(),
  }).optional(),
  instance_retirement_intent: z.object({ system_disk_id: text, disk_ownership_revision: integer.positive() }).optional(),
  workload_scale_intent: z.object({
    uid: text, namespace: text, kind: text, name: text, current_replicas: integer, replicas: integer,
  }).optional(),
  image_copy_intent: z.object({
    account: text, region: text, asset_id: text, source_revision: integer, destination_account: text,
    destination_region: text, name: text, can_retry_observation: flag.optional(),
  }).optional(),
  image_creation_intent: z.object({ name: text, instance_id: text, instance_revision: integer }).optional(),
  image_deletion_intent: z.object({ name: text, ownership_revision: integer }).optional(),
  snapshot_intent: z.object({
    name: text, volume_id: text, volume_revision: integer, allow_attached: flag.optional(),
    class: text.optional(), can_retry_observation: flag.optional(),
  }).optional(),
  snapshot_restore_intent: z.object({
    snapshot_id: text, snapshot_class: text, source_volume_id: text, source_volume_revision: integer,
    name: text, zone: text, disk_type: text, size_gib: integer, encrypted: flag,
  }).optional(),
}
