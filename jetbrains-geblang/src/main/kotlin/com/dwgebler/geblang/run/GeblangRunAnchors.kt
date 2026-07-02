package com.dwgebler.geblang.run

import com.dwgebler.geblang.highlighting.GeblangTokenTypes
import com.intellij.psi.PsiElement
import com.intellij.psi.TokenType
import com.intellij.psi.tree.IElementType

/**
 * Shared flat-leaf-stream anchor detection for Geblang's run/debug gutter markers,
 * used by both [GeblangRunLineMarkerContributor] (per-leaf `getInfo` checks) and
 * [GeblangFileRunConfigurationProducer] (scanning a whole file for a top-level
 * `func main(`).
 *
 * The Geblang PSI tree is FLAT (see [com.dwgebler.geblang.psi.GeblangParserDefinition]):
 * every leaf in a `.gb` file is a direct child of the file, in source order, with no
 * nesting - so "is this leaf a `main` declaration" is answered entirely by walking
 * [PsiElement.getPrevSibling]/[PsiElement.getNextSibling] from that leaf, skipping
 * over WHITESPACE/comment leaves along the way.
 */
internal object GeblangRunAnchors {

    /** The `main` IDENTIFIER leaf of a `func main(` declaration. */
    fun isMainAnchor(element: PsiElement): Boolean {
        if (element.node?.elementType != GeblangTokenTypes.IDENTIFIER) return false
        if (element.text != "main") return false
        return isFuncNameAnchorShape(element)
    }

    /**
     * The method-name IDENTIFIER leaf of an `@test`-decorated `func <name>(`
     * declaration: same `func ... (` shape as [isMainAnchor] (for any name), plus a
     * `@test` DECORATOR leaf immediately preceding (modulo whitespace/comments) the
     * `func` keyword itself.
     */
    fun isTestMethodAnchor(element: PsiElement): Boolean {
        if (element.node?.elementType != GeblangTokenTypes.IDENTIFIER) return false
        if (!isFuncNameAnchorShape(element)) return false
        val funcKeyword = prevNonTrivial(element) ?: return false
        val decorator = prevNonTrivial(funcKeyword) ?: return false
        return decorator.node?.elementType == GeblangTokenTypes.DECORATOR && decorator.text == "@test"
    }

    /**
     * The class-name IDENTIFIER leaf of a `class <Name> extends test.Test`
     * declaration: `class` KEYWORD before, and `extends test . Test` (KEYWORD,
     * IDENTIFIER, `.` OPERATOR, IDENTIFIER) after.
     */
    fun isTestClassAnchor(element: PsiElement): Boolean {
        if (element.node?.elementType != GeblangTokenTypes.IDENTIFIER) return false

        val prev = prevNonTrivial(element) ?: return false
        if (prev.node?.elementType != GeblangTokenTypes.KEYWORD || prev.text != "class") return false

        val extendsKeyword = nextNonTrivial(element) ?: return false
        if (extendsKeyword.node?.elementType != GeblangTokenTypes.KEYWORD || extendsKeyword.text != "extends") {
            return false
        }

        val moduleIdentifier = nextNonTrivial(extendsKeyword) ?: return false
        if (moduleIdentifier.node?.elementType != GeblangTokenTypes.IDENTIFIER || moduleIdentifier.text != "test") {
            return false
        }

        val dot = nextNonTrivial(moduleIdentifier) ?: return false
        if (dot.node?.elementType != GeblangTokenTypes.OPERATOR || dot.text != ".") return false

        val testIdentifier = nextNonTrivial(dot) ?: return false
        return testIdentifier.node?.elementType == GeblangTokenTypes.IDENTIFIER && testIdentifier.text == "Test"
    }

    /**
     * True if [file]'s flat leaf children contain a top-level `func main(`
     * declaration anywhere (used by [GeblangFileRunConfigurationProducer], which
     * receives an arbitrary context element inside the file rather than the `main`
     * leaf itself).
     */
    fun hasTopLevelMain(file: com.intellij.psi.PsiFile): Boolean =
        file.children.any { isMainAnchor(it) }

    /**
     * True if [file]'s flat leaf children contain a test-class or `@test`-method
     * anchor anywhere (used by [GeblangTestRunConfigurationProducer], which receives
     * an arbitrary context element inside the file rather than the anchor leaf
     * itself).
     */
    fun hasTestAnchor(file: com.intellij.psi.PsiFile): Boolean =
        file.children.any { isTestClassAnchor(it) || isTestMethodAnchor(it) }

    /** Shared shape check for `func <IDENTIFIER>(`: used by both func-name anchors. */
    private fun isFuncNameAnchorShape(element: PsiElement): Boolean {
        val prev = prevNonTrivial(element) ?: return false
        if (prev.node?.elementType != GeblangTokenTypes.KEYWORD || prev.text != "func") return false
        val next = nextNonTrivial(element) ?: return false
        return next.node?.elementType == GeblangTokenTypes.LPAREN
    }

    /** Walks backward over WHITESPACE/comment leaves to the nearest meaningful sibling. */
    private fun prevNonTrivial(element: PsiElement): PsiElement? {
        var current = element.prevSibling
        while (current != null && isTrivial(current.node?.elementType)) {
            current = current.prevSibling
        }
        return current
    }

    /** Walks forward over WHITESPACE/comment leaves to the nearest meaningful sibling. */
    private fun nextNonTrivial(element: PsiElement): PsiElement? {
        var current = element.nextSibling
        while (current != null && isTrivial(current.node?.elementType)) {
            current = current.nextSibling
        }
        return current
    }

    /**
     * True for whitespace or comment leaves. Note: the platform's PsiBuilder-based
     * parsing infrastructure wraps whitespace tokens as `PsiWhiteSpaceImpl` with
     * element type [TokenType.WHITE_SPACE] - NOT the lexer's own
     * [GeblangTokenTypes.WHITESPACE] - so both must be checked (comment tokens are
     * not remapped this way and keep their original [GeblangTokenTypes] type).
     */
    private fun isTrivial(elementType: IElementType?): Boolean =
        elementType == TokenType.WHITE_SPACE ||
            elementType == GeblangTokenTypes.WHITESPACE ||
            elementType == GeblangTokenTypes.LINE_COMMENT ||
            elementType == GeblangTokenTypes.BLOCK_COMMENT
}
