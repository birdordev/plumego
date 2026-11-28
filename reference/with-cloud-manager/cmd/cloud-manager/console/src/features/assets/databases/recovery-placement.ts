import { z } from 'zod'
import type { Asset } from '../types'

type PlacementField = { name: string; label: string } & (
  { type: 'text'; pattern: z.ZodString } |
  { type: 'number'; min: number; max: number; step: number } |
  { type: 'select'; options: readonly [string, ...string[]] }
)

type Placement = {
  policy: string
  fields: readonly PlacementField[]
  storageType: string
  namePattern: RegExp
  nameMax: number
  timeStep: string
}

const uuid = z.string().regex(/^[a-f0-9]{8}(?:-[a-f0-9]{4}){3}-[a-f0-9]{12}$/)
const code = z.string().regex(/^[A-Za-z0-9][A-Za-z0-9_.-]{0,95}$/)
const vpc = z.string().regex(/^vpc-[a-z0-9]{1,123}$/)
const zone = { name: 'zone', label: '可用区', type: 'text', pattern: code } as const
const flavor = { name: 'flavor', label: '数据库规格', type: 'text', pattern: code } as const
const name = /^[A-Za-z][A-Za-z0-9_.-]{3,63}$/

const huawei: Placement = {
  policy: '恢复到运维配置中的独立 VPC，创建按需计费的 MySQL 单机实例。',
  storageType: 'CLOUDSSD', namePattern: name, nameMax: 64, timeStep: '0.001',
  fields: [
    { name: 'vpc_id', label: '目标 VPC ID', type: 'text', pattern: uuid },
    { name: 'subnet_id', label: '子网的网络 ID', type: 'text', pattern: uuid },
    zone, flavor,
    { name: 'security_group_id', label: '安全组 ID', type: 'text', pattern: uuid },
    { name: 'storage_gib', label: 'CLOUDSSD 容量（GiB）', type: 'number', min: 20, max: 10000, step: 10 },
  ],
}

const alibaba: Placement = {
  policy: '克隆阿里云基础版 MySQL ESSD 实例。恢复后使用指定 VPC，并核对继承的白名单和加密配置。',
  storageType: 'cloud_essd', namePattern: /^[A-Za-z][A-Za-z0-9_-]{3,63}$/, nameMax: 64, timeStep: '1',
  fields: [
    { name: 'vpc_id', label: '目标 VPC ID', type: 'text', pattern: vpc },
    { name: 'subnet_id', label: '目标 vSwitch ID', type: 'text', pattern: z.string().regex(/^vsw-[a-z0-9]{1,123}$/) },
    zone, flavor,
    { name: 'storage_gib', label: 'cloud_essd 容量（GiB）', type: 'number', min: 20, max: 64000, step: 5 },
  ],
}

const tencent: Placement = {
  policy: '在独立 VPC 中创建按量计费的双节点 MySQL 本地 SSD 实例。源实例必须关闭公网并且未开启加密。',
  storageType: 'local_ssd', namePattern: /^[A-Za-z][A-Za-z0-9_.-]{3,59}$/, nameMax: 60, timeStep: '1',
  fields: [
    { name: 'vpc_id', label: '目标 VPC ID', type: 'text', pattern: vpc },
    { name: 'subnet_id', label: '目标子网 ID', type: 'text', pattern: z.string().regex(/^subnet-[a-z0-9]{1,120}$/) },
    zone,
    { name: 'flavor', label: '数据库规格', type: 'select', options: ['UNIVERSAL', 'EXCLUSIVE'] },
    { name: 'security_group_id', label: '安全组 ID', type: 'text', pattern: z.string().regex(/^sg-[a-z0-9]{1,124}$/) },
    { name: 'cpu', label: 'CPU 核数', type: 'number', min: 1, max: 256, step: 1 },
    { name: 'memory_mib', label: '内存（MiB）', type: 'number', min: 1, max: 4194304, step: 1 },
    { name: 'storage_gib', label: '本地 SSD 容量（GiB）', type: 'number', min: 1, max: 64000, step: 1 },
  ],
}

export function recoveryPlacement(asset: Asset): Placement | undefined {
  if (asset.database_instance?.engine !== 'mysql') return undefined
  const product = asset.database_instance.product
  if (asset.provider === 'tencent-cvm' && product === 'tencent-cdb' && asset.id.startsWith('cdb-')) return tencent
  if (asset.provider === 'alibaba-ecs' && product === 'alibaba-rds') return alibaba
  if (asset.provider === 'huawei-ecs' && product === 'huawei-rds') return huawei
  return undefined
}

export function placementSchema(fields: readonly PlacementField[]) {
  return z.object(Object.fromEntries(fields.map((field) => [field.name, field.type === 'number'
    ? z.number().int().min(field.min).max(field.max).multipleOf(field.step)
    : field.type === 'select' ? z.enum(field.options) : field.pattern])))
}
