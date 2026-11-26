export function SelectField({ label, name, options }: {
  label: string
  name: string
  options: readonly (readonly [value: string, label: string])[]
}) {
  return (
    <label>
      {label}
      <select name={name}>
        {options.map(([value, text]) => <option key={value} value={value}>{text}</option>)}
      </select>
    </label>
  )
}
