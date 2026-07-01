package com.dwgebler.geblang.highlighting

import com.dwgebler.geblang.language.GeblangLanguage
import com.intellij.psi.tree.IElementType

/**
 * Token element types for the Geblang lexer.
 * Each constant corresponds to a lexical category in the language.
 */
class GeblangTokenType(debugName: String) : IElementType(debugName, GeblangLanguage)

object GeblangTokenTypes {
    @JvmField val WHITESPACE       = GeblangTokenType("WHITESPACE")
    @JvmField val LINE_COMMENT     = GeblangTokenType("LINE_COMMENT")     // # and ##
    @JvmField val BLOCK_COMMENT    = GeblangTokenType("BLOCK_COMMENT")    // /* */ and /** */
    @JvmField val STRING           = GeblangTokenType("STRING")           // all 4 string forms
    @JvmField val INTERPOLATION    = GeblangTokenType("INTERPOLATION")    // ${ ... } inside interpolated strings
    @JvmField val NUMBER           = GeblangTokenType("NUMBER")
    @JvmField val KEYWORD          = GeblangTokenType("KEYWORD")
    @JvmField val TYPE             = GeblangTokenType("TYPE")
    @JvmField val CONSTANT         = GeblangTokenType("CONSTANT")         // true false null this
    @JvmField val WORD_OPERATOR    = GeblangTokenType("WORD_OPERATOR")    // is not xor
    @JvmField val OPERATOR         = GeblangTokenType("OPERATOR")
    @JvmField val DECORATOR        = GeblangTokenType("DECORATOR")        // @name or @Foo.bar.baz
    @JvmField val IDENTIFIER       = GeblangTokenType("IDENTIFIER")
    @JvmField val LBRACE           = GeblangTokenType("LBRACE")
    @JvmField val RBRACE           = GeblangTokenType("RBRACE")
    @JvmField val LBRACKET         = GeblangTokenType("LBRACKET")
    @JvmField val RBRACKET         = GeblangTokenType("RBRACKET")
    @JvmField val LPAREN           = GeblangTokenType("LPAREN")
    @JvmField val RPAREN           = GeblangTokenType("RPAREN")
    @JvmField val BAD_CHARACTER    = GeblangTokenType("BAD_CHARACTER")
}
