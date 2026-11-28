export function SerialKeyField() {
  return <label>OpenSSH RSA 公钥（.pub）
    <textarea name="ssh_public_key" rows={3} maxLength={4096} required spellCheck={false} />
  </label>
}
