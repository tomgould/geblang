package com.dwgebler.geblang.psi

import com.dwgebler.geblang.highlighting.GeblangLexer
import com.dwgebler.geblang.highlighting.GeblangTokenTypes
import com.dwgebler.geblang.language.GeblangLanguage
import com.intellij.extapi.psi.ASTWrapperPsiElement
import com.intellij.lang.ASTNode
import com.intellij.lang.ParserDefinition
import com.intellij.lang.PsiBuilder
import com.intellij.lang.PsiParser
import com.intellij.lexer.Lexer
import com.intellij.openapi.project.Project
import com.intellij.psi.FileViewProvider
import com.intellij.psi.PsiElement
import com.intellij.psi.PsiFile
import com.intellij.psi.tree.IElementType
import com.intellij.psi.tree.IFileElementType
import com.intellij.psi.tree.TokenSet

/**
 * Minimal [ParserDefinition] for Geblang.
 *
 * This does NOT parse a grammar. It builds a FLAT PSI tree: a single
 * [GeblangFile] root whose children are one leaf PSI element per lexer
 * token, in source order, with no nesting. This gives the platform enough
 * of a PSI tree to hang folding, run-line markers, TODO highlighting and
 * spellchecking off of later, without committing to a real parser. All
 * semantic analysis remains owned by the Geblang LSP server.
 */
class GeblangParserDefinition : ParserDefinition {

    override fun createLexer(project: Project?): Lexer = GeblangLexer()

    override fun createParser(project: Project?): PsiParser =
        PsiParser { root: IElementType, builder: PsiBuilder ->
            val rootMarker = builder.mark()
            while (!builder.eof()) {
                builder.advanceLexer()
            }
            rootMarker.done(root)
            builder.treeBuilt
        }

    override fun getFileNodeType(): IFileElementType = FILE

    override fun getCommentTokens(): TokenSet =
        TokenSet.create(GeblangTokenTypes.LINE_COMMENT, GeblangTokenTypes.BLOCK_COMMENT)

    override fun getWhitespaceTokens(): TokenSet =
        TokenSet.create(GeblangTokenTypes.WHITESPACE)

    override fun getStringLiteralElements(): TokenSet =
        TokenSet.create(GeblangTokenTypes.STRING)

    override fun createElement(node: ASTNode): PsiElement = ASTWrapperPsiElement(node)

    override fun createFile(viewProvider: FileViewProvider): PsiFile = GeblangFile(viewProvider)

    companion object {
        /** File-level element type for the flat Geblang PSI tree. */
        @JvmField
        val FILE = IFileElementType(GeblangLanguage)
    }
}
