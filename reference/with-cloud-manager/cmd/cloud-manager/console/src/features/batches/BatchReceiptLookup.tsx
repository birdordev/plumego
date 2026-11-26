type Props = { id: string; busy: boolean; setID: (id: string) => void; lookup: () => void }

export function BatchReceiptLookup({ id, busy, setID, lookup }: Props) {
  return <form id="lookup-batch" onSubmit={(event) => { event.preventDefault(); lookup() }} className="filters batch-lookup">
    <label>批次 ID<input id="batch-id" name="id" value={id} onChange={(event) => setID(event.target.value)} required maxLength={128} /></label>
    <button disabled={busy}>查看批次</button>
  </form>
}
