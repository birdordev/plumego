const attempts = new Map<string, string>()

export function operationAttempt(identity: string) {
  const previous = attempts.get(identity)
  if (previous) return previous
  const key = crypto.randomUUID()
  attempts.set(identity, key)
  return key
}

export function removeOperationAttempt(identity: string, key: string) {
  if (attempts.get(identity) === key) attempts.delete(identity)
}

export function clearOperationAttempts() {
  attempts.clear()
}
