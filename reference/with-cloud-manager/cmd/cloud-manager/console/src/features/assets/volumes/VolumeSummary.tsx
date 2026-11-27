import type { Asset } from '../types'

export function volumeSummaryText(volume: Asset['volume']): string {
  if (!volume) return ''
  const usage = volume.usage === 'system' ? '系统盘' : volume.usage === 'data' ? '数据盘' : undefined
  const boot = volume.bootable === undefined ? undefined : volume.bootable ? '可启动' : '不可启动'
  const capacity = volume.size_gib === undefined ? undefined : `${volume.size_gib} GiB`
  return [capacity, volume.storage_class, usage, boot].filter(Boolean).join(' · ')
}

export function VolumeSummary({ volume }: { volume: NonNullable<Asset['volume']> }) {
  const details = volumeSummaryText(volume)
  return details ? <span className="minor">{details}</span> : null
}
