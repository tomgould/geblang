package com.dwgebler.geblang.editor

import com.intellij.lang.CodeDocumentationAwareCommenter
import com.intellij.psi.PsiComment
import com.intellij.psi.tree.IElementType
import com.dwgebler.geblang.highlighting.GeblangTokenTypes

/**
 * Commenter for Geblang. Ctrl+/ toggles `#` line comments.
 * Ctrl+Shift+/ toggles `/* */` block comments.
 *
 * Note: // is INTEGER DIVISION in Geblang — the line comment prefix is `#`.
 */
class GeblangCommenter : CodeDocumentationAwareCommenter {
    override fun getLineCommentPrefix(): String = "# "
    override fun getBlockCommentPrefix(): String = "/* "
    override fun getBlockCommentSuffix(): String = " */"
    override fun getCommentedBlockCommentPrefix(): String? = null
    override fun getCommentedBlockCommentSuffix(): String? = null

    // Documentation comment support
    override fun getDocumentationCommentPrefix(): String = "/** "
    override fun getDocumentationCommentLinePrefix(): String = " * "
    override fun getDocumentationCommentSuffix(): String = " */"
    override fun isDocumentationComment(element: PsiComment?): Boolean = false
    override fun getDocumentationCommentTokenType(): IElementType? = GeblangTokenTypes.BLOCK_COMMENT
    override fun getLineCommentTokenType(): IElementType = GeblangTokenTypes.LINE_COMMENT
    override fun getBlockCommentTokenType(): IElementType = GeblangTokenTypes.BLOCK_COMMENT
}
