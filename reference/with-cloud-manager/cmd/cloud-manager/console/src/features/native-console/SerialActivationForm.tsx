import { DetailForm } from '../../shared/detail/DetailForm'
import { formText } from '../../shared/detail/form-values'
import { openDetail } from '../../shared/detail/store'
import { request } from '../session/client'
import { grantsCollection } from './collection'
import { serialReceiptSchema } from './schema'
import { SerialKeyField } from './SerialKeyField'

export function SerialActivationForm({ target, revision, stamp }: { target: string; revision: number; stamp: number }) {
  return <DetailForm stamp={stamp} submitLabel="发放串口公钥" onSubmit={(data) => request(target + '/serial-key', {
    method: 'POST', match: revision, body: JSON.stringify({ ssh_public_key: formText(data, 'ssh_public_key').trim() }),
  }, serialReceiptSchema)} onSaved={({ ssh }) => {
    void grantsCollection.refresh()
    const { host, username, port, valid_until: until } = ssh
    openDetail('AWS 串口公钥已发放', () => <>
      <p>建连截止 {new Date(until).toLocaleTimeString('zh-CN')}</p>
      <code>ssh -o StrictHostKeyChecking=yes -o IdentitiesOnly=yes -i 本地私钥 -p {port} {username}@{host}</code>
    </>)
  }}>
    <p>发放后请立即使用对应的本地私钥连接。</p>
    <SerialKeyField />
  </DetailForm>
}
