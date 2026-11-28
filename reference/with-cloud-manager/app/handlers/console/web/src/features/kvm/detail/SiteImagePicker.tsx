import { DetailFields } from '../../../shared/components/DetailFields'
import { listImages } from '../provisioning/api'
import type { Image } from '../provisioning/schema'
import { PagedResults } from '../../../shared/collection/PagedResults'

export function SiteImagePicker({ sitePath, signal, chosen, choose }: {
  sitePath: string; signal: AbortSignal; chosen?: Image; choose: (image?: Image) => void
}) {
  if (chosen) return <section aria-label="已选镜像">
    <DetailFields fields={[
      ['镜像', chosen.image_id], ['大小（字节）', chosen.image_size_bytes], ['SHA-256', chosen.image_sha256],
    ]} />
    <button type="button" onClick={() => choose()}>更换镜像</button>
  </section>
  return <section aria-label="站点镜像">
    <h3>选择站点镜像</h3>
    <PagedResults target={sitePath} read={listImages} signal={signal}>{(images) => images.length
      ? <ul className="row-actions">{images.map((image) => <li key={image.image_id}>
        <button type="button" onClick={() => choose(image)}>{image.image_id}</button>
      </li>)}</ul> : <p role="status">本站点暂无登记镜像。</p>}</PagedResults>
  </section>
}
