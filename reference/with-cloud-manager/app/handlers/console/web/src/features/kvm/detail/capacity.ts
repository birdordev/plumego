import type { DetailField } from '../../../shared/components/DetailFields'

export function capacityFields(capacity: { vcpu: number; memory_mib: number; disk_mib: number; gpu: number }): DetailField[] {
  return [['vCPU', capacity.vcpu], ['内存（MiB）', capacity.memory_mib], ['磁盘（MiB）', capacity.disk_mib], ['GPU', capacity.gpu]]
}
