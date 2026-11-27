export function SelectField({ label, name, options, initialValue }: {
  label: string
  name: string
  options: readonly (readonly [value: string, label: string])[]
  initialValue?: string | null
}) {
  return (
    <label>
      {label}
      <select name={name} defaultValue={options.find(([value]) => value === initialValue)?.[0]}>
        {options.map(([value, text]) => <option key={value} value={value}>{text}</option>)}
      </select>
    </label>
  )
}
