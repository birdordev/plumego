import type { ListAssetActions } from '../row-action'
import { canCreateImage, canDeleteImage, showImageCopy, showImageCreation, showImageDeletion } from './actions'

export const imageCreationActions: ListAssetActions = (asset, { canExecute }) => canExecute && canCreateImage(asset)
  ? [{ label: '制作系统盘镜像', run: () => showImageCreation(asset) }]
  : []

export const imageActions: ListAssetActions = (asset, { canExecute }) => canExecute && canDeleteImage(asset)
  ? [{ label: '申请复制镜像', run: () => showImageCopy(asset) }, { label: '申请删除镜像', run: () => showImageDeletion(asset) }]
  : []
