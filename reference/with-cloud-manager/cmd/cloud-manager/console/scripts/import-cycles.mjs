export function firstImportCycle(graph) {
  const complete = new Set()
  const stack = new Map()

  function visit(file) {
    if (stack.has(file)) return [...Array.from(stack.keys()).slice(stack.get(file)), file]
    if (complete.has(file)) return
    stack.set(file, stack.size)

    for (const dependency of graph.get(file) ?? []) {
      const cycle = visit(dependency)
      if (cycle) return cycle
    }

    stack.delete(file)
    complete.add(file)
  }

  for (const file of graph.keys()) {
    const cycle = visit(file)
    if (cycle) return cycle
  }
}
