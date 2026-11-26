type Summary = {
  product?: string
  dns_name?: string
  private_addresses?: string[]
}

export function LoadBalancerSummary({ product, dns_name, private_addresses }: Summary) {
  return <>
    {product ? <span className="minor">{product}</span> : null}
    {dns_name ? <span className="minor">{dns_name}</span> : null}
    {private_addresses?.length ? <span className="minor">私网 {private_addresses.join(' · ')}</span> : null}
  </>
}
