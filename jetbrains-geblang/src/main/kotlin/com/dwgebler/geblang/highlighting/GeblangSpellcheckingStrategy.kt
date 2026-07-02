package com.dwgebler.geblang.highlighting

import com.intellij.psi.PsiElement
import com.intellij.psi.tree.IElementType
import com.intellij.spellchecker.inspections.IdentifierSplitter
import com.intellij.spellchecker.tokenizer.SpellcheckingStrategy
import com.intellij.spellchecker.tokenizer.Tokenizer
import com.intellij.spellchecker.tokenizer.TokenizerBase

/**
 * Spellchecking support for Geblang, built on the same flat-token PSI leaves
 * described in [com.dwgebler.geblang.psi.GeblangParserDefinition]: one leaf
 * PSI element per lexer token, in source order, no grammar/nesting.
 *
 * - Comment leaves (`LINE_COMMENT`, `BLOCK_COMMENT`) are spellchecked as
 *   free-form prose via [SpellcheckingStrategy.TEXT_TOKENIZER].
 * - String leaves (`STRING`) are spellchecked the same way, so misspelled
 *   words inside string literals are still flagged.
 * - Identifier leaves (`IDENTIFIER`) are spellchecked with a word splitter
 *   that breaks camelCase/snake_case names into their constituent words,
 *   so e.g. `userNaem` flags "Naem" without flagging the whole identifier
 *   as one long unknown word.
 * - Everything else (keywords, operators, numbers, decorators, braces,
 *   whitespace, bad characters) is never spellchecked.
 */
class GeblangSpellcheckingStrategy : SpellcheckingStrategy() {

    private val identifierTokenizer: Tokenizer<PsiElement> =
        TokenizerBase.create(IdentifierSplitter.getInstance())

    override fun getTokenizer(element: PsiElement): Tokenizer<*> {
        val type: IElementType = element.node?.elementType ?: return EMPTY_TOKENIZER

        return when (type) {
            GeblangTokenTypes.LINE_COMMENT, GeblangTokenTypes.BLOCK_COMMENT -> TEXT_TOKENIZER
            GeblangTokenTypes.STRING -> TEXT_TOKENIZER
            GeblangTokenTypes.IDENTIFIER -> identifierTokenizer
            else -> EMPTY_TOKENIZER
        }
    }
}
