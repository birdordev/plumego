import type { z } from 'zod'
import type { Asset } from '../types'
import { showLoadBalancerBackendDetach, showLoadBalancerBackendWeight } from './members'
import type { configurationSchema, TargetGroup } from './schema'

type Backends = NonNullable<z.infer<typeof configurationSchema>['backends']>
const healthLabels: Partial<Record<string, string>> = {
  healthy: '健康', unhealthy: '异常', initial: '初始化', disabled: '未启用', unused: '未使用', unknown: '未知',
}
const roleLabels: Partial<Record<string, string>> = { primary: '主', secondary: '备' }

function portLabel(port: number) {
  return port === 0 ? '动态端口' : '端口 ' + port
}

export function BackendSections({ asset, backends, groups, editable }:
  { asset: Asset; backends: Backends; groups: TargetGroup[]; editable: boolean }) {
  const groupsByID = new Map(groups.map((item) => [item.id, item]))
  return <>
    <h3>后端成员</h3>
    {backends.members.length ? <ul>{backends.members.map((item) => {
      const group = groupsByID.get(item.group_id)
      const port = group?.kind === 'default' && item.port === 0 ? '端口由监听器配置' : portLabel(item.port)
      return <li key={JSON.stringify([item.group_id, item.key, item.port])}>
        {[item.group_id, item.key, roleLabels[item.role ?? ''], port, item.weight === undefined ? '' : '权重 ' + item.weight].filter(Boolean).join(' · ')}
        {editable && item.weight_editable && group?.exclusive ? <>
          {' '}<button type="button" onClick={() => showLoadBalancerBackendWeight(asset, item)}>申请修改权重</button>
          {item.weight === 0 ? <> <button type="button" onClick={() => showLoadBalancerBackendDetach(asset, item)}>申请解绑成员</button></> : null}
        </> : null}
      </li>
    })}</ul> : <p>无记录</p>}
    <h3>已报告的健康检查</h3>
    {backends.checks.length ? <ul>{backends.checks.map((item) =>
      <li key={JSON.stringify([item.listener_id, item.listener_port, item.rule_id, item.group_id, item.key, item.port])}>
        {[item.listener_id, item.listener_port && '监听端口 ' + item.listener_port, item.rule_id, item.group_id,
          item.key, portLabel(item.port), healthLabels[item.status] || '未知'].filter(Boolean).join(' · ')}
      </li>,
    )}</ul> : <p>没有报告健康结果</p>}
  </>
}
