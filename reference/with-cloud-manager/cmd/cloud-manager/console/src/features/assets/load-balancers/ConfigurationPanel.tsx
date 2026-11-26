import { ConsoleRequestError, requestErrorMessage } from '../../../shared/api/requests'
import { useDetailRead } from '../../../shared/detail/useDetailRead'
import { timestamp } from '../../../shared/display/labels'
import { request } from '../../session/client'
import { canExecute } from '../../session/project'
import type { Asset } from '../types'
import { showLoadBalancerBackendAttach } from './attach'
import { BackendSections } from './BackendSections'
import { showLoadBalancerRename } from './rename'
import { configurationSchema } from './schema'

const groupTypes: Partial<Record<string, string>> = {
  default: '默认后端组', 'primary-secondary': '主备后端组', virtual: '虚拟服务器组', direct: '直接绑定',
}

function readConfiguration(target: string, signal: AbortSignal) {
  return request(target, { signal }, configurationSchema)
}

export default function ConfigurationPanel({ target, asset, signal }: { target: string; asset: Asset; signal: AbortSignal }) {
  const result = useDetailRead(target, signal, readConfiguration)
  if (result.state === 'loading') return <p role="status">正在读取最近一次完整快照…</p>
  if (result.state === 'error') {
    const error = result.error
    const text = error instanceof ConsoleRequestError && error.status === 404 ? '暂时没有可用的配置快照。' : requestErrorMessage(error)
    return <p role="alert" className="notice" data-error="true">{text}</p>
  }

  const configuration = result.data
  const product = asset.load_balancer?.product
  const classic = product === 'alibaba-clb' || (product === 'tencent-clb' &&
    configuration.target_groups.some((group) => group.kind === 'default'))
  const editable = asset.ownership_revision > 0 && !classic && canExecute()

  return <>
    <p>{asset.name || asset.id} · 采集时间 {timestamp(configuration.observed_at)}</p>
    <h3>监听器</h3>
    {configuration.listeners.length ? <ul>{configuration.listeners.map((item) => {
      const ports = item.end_port && item.end_port !== item.port ? item.port + '–' + item.end_port : String(item.port)
      return <li key={item.id}>
        {[item.name || item.id, item.protocol + ' ' + ports, item.target_group_ids?.join('、')].filter(Boolean).join(' · ')}
        {editable ? <> <button type="button" onClick={() => showLoadBalancerRename(asset, item)}>申请监听器改名</button></> : null}
      </li>
    })}</ul> : <p>无记录</p>}
    <h3>后端组</h3>
    {configuration.target_groups.length ? <ul>{configuration.target_groups.map((item) => <li key={item.id}>
      {[item.name, groupTypes[item.kind ?? ''], item.id, item.protocol, item.algorithm].filter(Boolean).join(' · ')}
      {editable && item.exclusive ? <> <button type="button" onClick={() => showLoadBalancerRename(asset, item, true)}>申请后端组改名</button>
        {configuration.backends ? <> <button type="button" onClick={() => showLoadBalancerBackendAttach(asset, item)}>申请添加后端</button></> : null}
      </> : null}
    </li>)}</ul> : <p>无记录</p>}
    {configuration.backends ? <BackendSections asset={asset} editable={editable} backends={configuration.backends}
      groups={configuration.target_groups} /> : null}
  </>
}
