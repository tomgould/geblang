package com.dwgebler.geblang.editor

import com.dwgebler.geblang.highlighting.GeblangTokenTypes
import com.intellij.lang.ASTNode
import com.intellij.lang.folding.FoldingBuilderEx
import com.intellij.lang.folding.FoldingDescriptor
import com.intellij.openapi.editor.Document
import com.intellij.openapi.project.DumbAware
import com.intellij.openapi.util.TextRange
import com.intellij.psi.PsiElement

/**
 * Folding for Geblang: `{ ... }` blocks and multi-line `/* ... */` comments.
 *
 * The Geblang PSI tree is FLAT (see [com.dwgebler.geblang.psi.GeblangParserDefinition]):
 * the file's direct children are one leaf node per lexer token, in source
 * order, with no nesting. Folding therefore cannot walk a grammar tree - it
 * scans that flat leaf stream directly:
 *
 *  - Brace blocks are found by tracking a depth counter over LBRACE/RBRACE
 *    leaves. Each matched pair becomes one fold region spanning from the
 *    `{` to its matching `}` (inclusive). Nesting falls out naturally: an
 *    inner matched pair produces its own (nested) descriptor.
 *  - Block comments are single BLOCK_COMMENT leaves; any one that spans more
 *    than one line becomes a fold region over its own range.
 *
 * Single-line `{}` and single-line block comments are never folded.
 */
class GeblangFoldingBuilder : FoldingBuilderEx(), DumbAware {

    override fun buildFoldRegions(root: PsiElement, document: Document, quick: Boolean): Array<FoldingDescriptor> {
        val leaves = root.node.getChildren(null)
        val descriptors = mutableListOf<FoldingDescriptor>()

        val braceStack = ArrayDeque<ASTNode>()
        for (leaf in leaves) {
            when (leaf.elementType) {
                GeblangTokenTypes.LBRACE -> braceStack.addLast(leaf)
                GeblangTokenTypes.RBRACE -> {
                    // Unbalanced closing brace with nothing to match - skip it,
                    // never throw.
                    val open = if (braceStack.isEmpty()) null else braceStack.removeLast()
                    if (open != null) {
                        val range = TextRange(open.startOffset, leaf.textRange.endOffset)
                        if (spansMultipleLines(range, document)) {
                            descriptors += FoldingDescriptor(open, range)
                        }
                    }
                }
                GeblangTokenTypes.BLOCK_COMMENT -> {
                    val range = leaf.textRange
                    if (spansMultipleLines(range, document)) {
                        descriptors += FoldingDescriptor(leaf, range)
                    }
                }
                else -> Unit
            }
        }
        // Any leftover entries in braceStack are unmatched '{' - skip them,
        // never throw.

        return descriptors.toTypedArray()
    }

    override fun getPlaceholderText(node: ASTNode): String =
        if (node.elementType == GeblangTokenTypes.BLOCK_COMMENT) "/*...*/" else "{...}"

    override fun isCollapsedByDefault(node: ASTNode): Boolean = false

    private fun spansMultipleLines(range: TextRange, document: Document): Boolean {
        if (range.endOffset > document.textLength || range.startOffset >= range.endOffset) return false
        val startLine = document.getLineNumber(range.startOffset)
        val endLine = document.getLineNumber(range.endOffset)
        return endLine > startLine
    }
}
