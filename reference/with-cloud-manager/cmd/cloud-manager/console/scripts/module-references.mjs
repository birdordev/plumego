import ts from 'typescript'

function onlyTypes(clause) {
  if (!clause) return false
  if (clause.isTypeOnly) return true
  const bindings = clause.namedBindings
  return !clause.name && bindings && ts.isNamedImports(bindings) &&
    bindings.elements.length > 0 && bindings.elements.every((element) => element.isTypeOnly)
}

export function moduleReferences(sourceFile) {
  const references = []

  function add(node, specifier, eager = false) {
    const name = specifier && ts.isStringLiteralLike(specifier) ? specifier.text : undefined
    const { line } = sourceFile.getLineAndCharacterOfPosition(node.getStart(sourceFile))
    references.push({ name, line: line + 1, eager })
  }

  function visit(node) {
    if (ts.isImportDeclaration(node)) {
      add(node, node.moduleSpecifier, !onlyTypes(node.importClause))
    } else if (ts.isExportDeclaration(node) && node.moduleSpecifier) {
      const clause = node.exportClause
      const types = clause && ts.isNamedExports(clause) && clause.elements.length > 0 &&
        clause.elements.every((element) => element.isTypeOnly)
      add(node, node.moduleSpecifier, !node.isTypeOnly && !types)
    } else if (ts.isImportTypeNode(node) && ts.isLiteralTypeNode(node.argument)) {
      add(node, node.argument.literal)
    } else if (ts.isImportEqualsDeclaration(node) && ts.isExternalModuleReference(node.moduleReference)) {
      add(node, node.moduleReference.expression, !node.isTypeOnly)
    } else if (ts.isCallExpression(node) && node.expression.kind === ts.SyntaxKind.ImportKeyword) {
      add(node, node.arguments[0])
    } else if (ts.isCallExpression(node) && ts.isPropertyAccessExpression(node.expression) &&
      ts.isMetaProperty(node.expression.expression) && node.expression.expression.keywordToken === ts.SyntaxKind.ImportKeyword &&
      node.expression.name.text === 'glob') {
      add(node)
    }
    ts.forEachChild(node, visit)
  }

  visit(sourceFile)
  return references
}
