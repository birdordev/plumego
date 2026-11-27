import { DetailForm } from '../../../shared/detail/DetailForm'
import { formText } from '../../../shared/detail/form-values'
import { openDetail } from '../../../shared/detail/store'
import { grantsCollection } from '../grants/collection'
import { SerialKeyField } from '../SerialKeyField'
import { activateSerialKey } from './api'

export function SerialActivationForm({ target, revision, stamp }: { target: string; revision: number; stamp: number }) {
  return <DetailForm stamp={stamp} submitLabel="发放串口公钥"
    onSubmit={(data) => activateSerialKey(target, revision, formText(data, 'ssh_public_key').trim())} onSaved={({ ssh }) => {
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
