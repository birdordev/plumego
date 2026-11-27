import { DetailForm } from '../../../../shared/detail/DetailForm'
import { formText } from '../../../../shared/detail/form-values'
import { registerDeployment } from './api'
import { registrationSchema } from './schema'

type Props = { project: string; stage: string; boot: string; stamp: number; reload: () => void }

export function DeploymentRegistration({ project, stage, boot, stamp, reload }: Props) {
  async function submit(form: FormData) {
    const parsed = registrationSchema.safeParse({
      stage_operation: stage,
      boot_operation: boot,
      installer_id: formText(form, 'installer').trim(),
      image_sha256: formText(form, 'image').trim().toLowerCase(),
    })
    if (!parsed.success) throw new Error('请填写有效的安装器服务主体 ID 和 64 位镜像 SHA-256。')
    return registerDeployment(project, parsed.data)
  }

  return <>
    <p>一次性 PXE 操作 <code>{stage}</code></p>
    <p>登记后由独立审批人核对并批准开机，安装结果由指定的服务主体上报。</p>
    <DetailForm stamp={stamp} submitLabel="登记部署" onSubmit={submit} onSaved={reload}>
      <label>安装器服务主体 ID<input name="installer" autoComplete="off" required maxLength={128} /></label>
      <label>安装镜像 SHA-256<input name="image" autoComplete="off" spellCheck={false} required maxLength={64} /></label>
    </DetailForm>
  </>
}
