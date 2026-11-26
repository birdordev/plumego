type Props = { parentID?: string; snapshot?: { class?: string; size_gib?: number } }

export function SnapshotSummary({ parentID, snapshot }: Props) {
  const summary = [
    parentID && '源卷 ' + parentID,
    snapshot?.class,
    snapshot?.size_gib && snapshot.size_gib + ' GiB',
  ].filter(Boolean).join(' · ')

  return summary ? <span className="minor">{summary}</span> : null
}
