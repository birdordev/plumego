import { projectPath } from '../../features/session/project'
import type { Session } from '../../features/session/store'

export const isAdmin = ({ project }: Session) => project.role === 'admin'

export function consoleFlags({ profile }: Session) {
  return {
    nativeConsole: Boolean(profile.native_console),
    nativeSerial: Boolean(profile.native_serial_console),
  }
}

export const noProps = () => ({})
export const projectProps = ({ session }: { session: Session }) => ({ base: projectPath(session) })
export const adminProps = ({ session }: { session: Session }) => ({ admin: isAdmin(session) })
