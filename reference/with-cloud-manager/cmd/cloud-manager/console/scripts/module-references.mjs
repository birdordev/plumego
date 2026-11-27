import ts from 'typescript'

export function moduleReferences(sourceFile) {
  const references = []

  function add(node, specifier, kind) {
    const name = specifier && ts.isStringLiteralLike(specifier) ? specifier.text : undefined
    const { line } = sourceFile.getLineAndCharacterOfPosition(node.getStart(sourceFile))
    references.push({ name, line: line + 1, kind })
  }

  function visit(node) {
    if (ts.isImportDeclaration(node)) {
      add(node, node.moduleSpecifier, node.importClause?.isTypeOnly ? 'type' : 'static')
    } else if (ts.isExportDeclaration(node) && node.moduleSpecifier) {
      add(node, node.moduleSpecifier, node.isTypeOnly ? 'type' : 'static')
    } else if (ts.isImportTypeNode(node) && ts.isLiteralTypeNode(node.argument)) {
      add(node, node.argument.literal, 'type')
    } else if (ts.isImportEqualsDeclaration(node) && ts.isExternalModuleReference(node.moduleReference)) {
      add(node, node.moduleReference.expression, node.isTypeOnly ? 'type' : 'static')
    } else if (ts.isCallExpression(node) && node.expression.kind === ts.SyntaxKind.ImportKeyword) {
      add(node, node.arguments[0], 'dynamic')
    } else if (ts.isCallExpression(node) && ts.isPropertyAccessExpression(node.expression) &&
      ts.isMetaProperty(node.expression.expression) && node.expression.expression.keywordToken === ts.SyntaxKind.ImportKeyword &&
      node.expression.name.text === 'glob') {
      add(node, undefined, 'dynamic')
    }
    ts.forEachChild(node, visit)
  }

  visit(sourceFile)
  return references
}
