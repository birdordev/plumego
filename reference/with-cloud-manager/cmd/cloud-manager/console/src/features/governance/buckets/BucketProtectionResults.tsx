import type { ComponentProps } from 'react'
import { ConsoleTable } from '../../../shared/components/ConsoleTable'
import { MetricStrip } from '../../../shared/components/MetricStrip'
import { bucketEncryptionLabels, bucketVersioningLabels } from '../../../shared/display/bucket-settings'
import { timestamp } from '../../../shared/display/time'
import type { BucketProtectionReport } from './api'

const providerLabels = { 'tencent-cvm': '腾讯云 COS', 'alibaba-ecs': '阿里云 OSS', 'huawei-ecs': '华为云 OBS' }

export type BucketAssetLink = (bucket: { id: string; account: string }) => Pick<ComponentProps<'a'>, 'href' | 'onClick'>

export function BucketProtectionResults({ report, assetLink }: { report: BucketProtectionReport; assetLink: BucketAssetLink }) {
  const { summary, items } = report
  return <>
    <section aria-label="对象桶防护汇总">
      <MetricStrip values={[
        ['可见对象桶', summary.total], ['资产盘点待确认', summary.inventory_unavailable],
        ['明确未配置默认加密', summary.encryption.flagged], ['版本控制未启用', summary.versioning.flagged],
      ]} />
      <p className="minor">统计时间 {timestamp(report.as_of)}</p>
      <ConsoleTable columns={['观测项', '有效', '过期', '未取得有效观测']} rows={[
        { key: 'encryption', cells: ['默认加密', summary.encryption.fresh, summary.encryption.stale, summary.encryption.unobserved] },
        { key: 'versioning', cells: ['版本控制', summary.versioning.fresh, summary.versioning.stale, summary.versioning.unobserved] },
      ]} />
    </section>
    <section aria-label="对象桶防护明细">
      <ConsoleTable columns={['对象桶', '云厂商', '账号', '实际地域', '资产盘点', '默认加密', '版本控制']}
        rows={items.map((item) => ({
          key: item.account + '/' + item.id,
          cells: [<a {...assetLink(item)}>{item.name}</a>, providerLabels[item.provider], item.account, item.location,
            item.inventory_available ? '可用' : '待确认',
            <Observation observation={item.encryption} labels={bucketEncryptionLabels} />,
            <Observation observation={item.versioning} labels={bucketVersioningLabels} />],
        }))} />
    </section>
  </>
}

type ObservationValue<State extends string> = { status: 'unobserved' } | { status: 'fresh' | 'stale'; state: State; observed_at: string }

function Observation<State extends string>({ observation, labels }: { observation: ObservationValue<State>; labels: Record<State, string> }) {
  if (observation.status === 'unobserved') return <>暂无观测</>
  return <>{labels[observation.state]}<span className="minor">
    {observation.status === 'stale' && '已过期 · '}{timestamp(observation.observed_at)}
  </span></>
}
