import { Component, type ReactNode } from 'react'

export class FeatureBoundary extends Component<{ children: ReactNode; message?: string }, { failed: boolean }> {
  state = { failed: false }

  static getDerivedStateFromError() {
    return { failed: true }
  }

  render() {
    if (this.state.failed) {
      return <p role="alert">{this.props.message ?? '无法加载详情，请刷新页面后重试。'}</p>
    }
    return this.props.children
  }
}
