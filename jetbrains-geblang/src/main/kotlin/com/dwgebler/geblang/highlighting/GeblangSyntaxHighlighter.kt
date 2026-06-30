package com.dwgebler.geblang.highlighting

import com.intellij.lexer.Lexer
import com.intellij.openapi.editor.DefaultLanguageHighlighterColors
import com.intellij.openapi.editor.HighlighterColors
import com.intellij.openapi.editor.colors.TextAttributesKey
import com.intellij.openapi.editor.colors.TextAttributesKey.createTextAttributesKey
import com.intellij.openapi.fileTypes.SyntaxHighlighterBase
import com.intellij.psi.tree.IElementType

/**
 * Maps Geblang token types to IntelliJ color-scheme attribute keys.
 */
class GeblangSyntaxHighlighter : SyntaxHighlighterBase() {

    override fun getHighlightingLexer(): Lexer = GeblangLexer()

    override fun getTokenHighlights(tokenType: IElementType): Array<TextAttributesKey> {
        return when (tokenType) {
            GeblangTokenTypes.LINE_COMMENT,
            GeblangTokenTypes.BLOCK_COMMENT  -> COMMENT_KEYS
            GeblangTokenTypes.STRING         -> STRING_KEYS
            GeblangTokenTypes.NUMBER         -> NUMBER_KEYS
            GeblangTokenTypes.KEYWORD        -> KEYWORD_KEYS
            GeblangTokenTypes.TYPE           -> TYPE_KEYS
            GeblangTokenTypes.CONSTANT       -> CONSTANT_KEYS
            GeblangTokenTypes.WORD_OPERATOR  -> WORD_OP_KEYS
            GeblangTokenTypes.OPERATOR,
            GeblangTokenTypes.LBRACE,
            GeblangTokenTypes.RBRACE,
            GeblangTokenTypes.LBRACKET,
            GeblangTokenTypes.RBRACKET,
            GeblangTokenTypes.LPAREN,
            GeblangTokenTypes.RPAREN         -> OPERATOR_KEYS
            GeblangTokenTypes.IDENTIFIER     -> IDENTIFIER_KEYS
            GeblangTokenTypes.BAD_CHARACTER  -> BAD_CHAR_KEYS
            else                             -> EMPTY_KEYS
        }
    }

    companion object {
        @JvmField val COMMENT    = createTextAttributesKey("GEBLANG_COMMENT",    DefaultLanguageHighlighterColors.LINE_COMMENT)
        @JvmField val STRING     = createTextAttributesKey("GEBLANG_STRING",     DefaultLanguageHighlighterColors.STRING)
        @JvmField val NUMBER     = createTextAttributesKey("GEBLANG_NUMBER",     DefaultLanguageHighlighterColors.NUMBER)
        @JvmField val KEYWORD    = createTextAttributesKey("GEBLANG_KEYWORD",    DefaultLanguageHighlighterColors.KEYWORD)
        @JvmField val TYPE       = createTextAttributesKey("GEBLANG_TYPE",       DefaultLanguageHighlighterColors.CLASS_NAME)
        @JvmField val CONSTANT   = createTextAttributesKey("GEBLANG_CONSTANT",   DefaultLanguageHighlighterColors.CONSTANT)
        @JvmField val WORD_OP    = createTextAttributesKey("GEBLANG_WORD_OP",    DefaultLanguageHighlighterColors.KEYWORD)
        @JvmField val OPERATOR   = createTextAttributesKey("GEBLANG_OPERATOR",   DefaultLanguageHighlighterColors.OPERATION_SIGN)
        @JvmField val IDENTIFIER = createTextAttributesKey("GEBLANG_IDENTIFIER", DefaultLanguageHighlighterColors.IDENTIFIER)
        @JvmField val BAD_CHAR   = createTextAttributesKey("GEBLANG_BAD_CHAR",   HighlighterColors.BAD_CHARACTER)

        private val COMMENT_KEYS    = arrayOf(COMMENT)
        private val STRING_KEYS     = arrayOf(STRING)
        private val NUMBER_KEYS     = arrayOf(NUMBER)
        private val KEYWORD_KEYS    = arrayOf(KEYWORD)
        private val TYPE_KEYS       = arrayOf(TYPE)
        private val CONSTANT_KEYS   = arrayOf(CONSTANT)
        private val WORD_OP_KEYS    = arrayOf(WORD_OP)
        private val OPERATOR_KEYS   = arrayOf(OPERATOR)
        private val IDENTIFIER_KEYS = arrayOf(IDENTIFIER)
        private val BAD_CHAR_KEYS   = arrayOf(BAD_CHAR)
        private val EMPTY_KEYS      = emptyArray<TextAttributesKey>()
    }
}
