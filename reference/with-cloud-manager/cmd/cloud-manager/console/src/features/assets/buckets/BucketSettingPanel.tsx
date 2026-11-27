import { useState } from 'react'
import { ConsoleRequestError, requestErrorMessage } from '../../../shared/api/requests'
import { useResourceRead } from '../../../shared/api/useResourceRead'
import { DetailFields } from '../../../shared/components/DetailFields'
import { bucketEncryptionLabels, bucketVersioningLabels } from '../../../shared/display/bucket-settings'
import { timestamp } from '../../../shared/display/time'
import { request } from '../../session/client'
import { bucketEncryptionSchema, bucketVersioningSchema } from './schema'

const readers = {
  versioning: async (target: string, signal: AbortSignal) => {
    const snapshot = await request(target, { signal }, bucketVersioningSchema)
    return { state: bucketVersioningLabels[snapshot.state], observedAt: snapshot.observed_at }
  },
  encryption: async (target: string, signal: AbortSignal) => {
    const snapshot = await request(target, { signal }, bucketEncryptionSchema)
    return { state: bucketEncryptionLabels[snapshot.state], observedAt: snapshot.observed_at }
  },
}

type Props = { target: string; name: string; signal: AbortSignal; setting: keyof typeof readers }
const headings = { versioning: '版本控制', encryption: '默认加密' }

export default function BucketSettingPanel(props: Props) {
  const [reload, setReload] = useState(0)
  return <>
    <BucketSettingSnapshot key={reload} {...props} />
    <button type="button" onClick={() => setReload((value) => value + 1)}>重新读取观测</button>
  </>
}

function BucketSettingSnapshot({ target, name, setting, signal }: Props) {
  const result = useResourceRead<{ state: string; observedAt: string }>(target, readers[setting], signal)
  const heading = headings[setting]
  if (result.state === 'loading') return <p role="status">正在读取{heading}状态…</p>
  if (result.state === 'error') {
    if (result.error instanceof ConsoleRequestError && result.error.status === 404) {
      return <p role="status">当前没有可用的{heading}观测。</p>
    }
    return <p role="alert">{requestErrorMessage(result.error)}</p>
  }
  return <DetailFields fields={[
    ['对象桶', name], [heading, result.data.state], ['观测时间', timestamp(result.data.observedAt)],
  ]} />
}
