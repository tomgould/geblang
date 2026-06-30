package com.dwgebler.geblang.editor

import com.dwgebler.geblang.highlighting.GeblangTokenTypes
import com.intellij.lang.BracePair
import com.intellij.lang.PairedBraceMatcher
import com.intellij.psi.PsiFile
import com.intellij.psi.tree.IElementType

/**
 * Provides brace matching for Geblang: {} [] ()
 * IntelliJ will highlight the matching brace when the cursor is adjacent to one.
 */
class GeblangBraceMatcher : PairedBraceMatcher {

    private val pairs = arrayOf(
        BracePair(GeblangTokenTypes.LBRACE,   GeblangTokenTypes.RBRACE,   true),
        BracePair(GeblangTokenTypes.LBRACKET, GeblangTokenTypes.RBRACKET, false),
        BracePair(GeblangTokenTypes.LPAREN,   GeblangTokenTypes.RPAREN,   false),
    )

    override fun getPairs(): Array<BracePair> = pairs
    override fun isPairedBracesAllowedBeforeType(lbraceType: IElementType, contextType: IElementType?): Boolean = true
    override fun getCodeConstructStart(file: PsiFile?, openingBraceOffset: Int): Int = openingBraceOffset
}
