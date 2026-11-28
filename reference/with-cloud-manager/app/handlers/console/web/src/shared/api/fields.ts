import { z } from 'zod'

export const account = z.string().min(1).max(128)
export const assetId = z.string().min(1).max(512)
export const revision = z.number().int().positive()
export const time = z.iso.datetime({ offset: true })
