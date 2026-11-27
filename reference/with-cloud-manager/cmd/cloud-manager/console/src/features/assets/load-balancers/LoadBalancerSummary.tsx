import type { LoadBalancerSummaryData } from './summary-schema'

export function LoadBalancerSummary({ product, dns_name, private_addresses }: LoadBalancerSummaryData) {
  return <>
    {product ? <span className="minor">{product}</span> : null}
    {dns_name ? <span className="minor">{dns_name}</span> : null}
    {private_addresses?.length ? <span className="minor">私网 {private_addresses.join(' · ')}</span> : null}
  </>
}
