import { sessionSnapshot, type Session } from './store'

export function projectPath({ profile, project }: Session) {
  return '/v1/tenants/' + encodeURIComponent(profile.tenant) + '/projects/' + encodeURIComponent(project.id)
}

export function projectURL() {
  const session = sessionSnapshot()
  if (!session) throw new Error('请先选择项目。')
  return projectPath(session)
}

export function projectCanExecute({ project }: Session) {
  return project.role === 'operator' || project.role === 'admin'
}

export function canExecute() {
  const session = sessionSnapshot()
  return session !== null && projectCanExecute(session)
}
