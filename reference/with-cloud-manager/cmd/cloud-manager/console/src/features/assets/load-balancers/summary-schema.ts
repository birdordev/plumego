import { z } from 'zod'

export const loadBalancerSummarySchema = z.object({
  product: z.string().optional(),
  dns_name: z.string().optional(),
  private_addresses: z.array(z.string()).optional(),
})

export type LoadBalancerSummaryData = z.infer<typeof loadBalancerSummarySchema>
