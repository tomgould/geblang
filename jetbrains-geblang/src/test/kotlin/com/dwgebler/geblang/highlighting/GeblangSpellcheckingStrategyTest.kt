package com.dwgebler.geblang.highlighting

import com.dwgebler.geblang.language.GeblangFileType
import com.intellij.spellchecker.tokenizer.SpellcheckingStrategy
import com.intellij.spellchecker.tokenizer.TokenizerBase
import com.intellij.testFramework.fixtures.BasePlatformTestCase

/**
 * Verifies [GeblangSpellcheckingStrategy.getTokenizer] against real leaves
 * pulled from the flat Geblang PSI (see
 * [com.dwgebler.geblang.psi.GeblangParserDefinition]): comment and string
 * leaves get a real (non-empty) tokenizer so their prose is spellchecked,
 * identifier leaves get a word-splitting tokenizer, and everything else
 * (keywords, operators, numbers) is excluded via [SpellcheckingStrategy.EMPTY_TOKENIZER].
 */
class GeblangSpellcheckingStrategyTest : BasePlatformTestCase() {

    private val strategy = GeblangSpellcheckingStrategy()

    private val snippet = """
        # a lowercase comment
        func setAge(int userAge): int {
            let greeting = "hello world"
            return userAge
        }
    """.trimIndent()

    private fun leafOfType(type: com.intellij.psi.tree.IElementType, text: String? = null) =
        myFixture.configureByText(GeblangFileType, snippet).children.first { child ->
            child.node.elementType == type && (text == null || child.text == text)
        }

    fun testLineCommentLeafUsesTextTokenizer() {
        val comment = leafOfType(GeblangTokenTypes.LINE_COMMENT)
        val tokenizer = strategy.getTokenizer(comment)
        assertSame(SpellcheckingStrategy.TEXT_TOKENIZER, tokenizer)
    }

    fun testStringLeafUsesTextTokenizer() {
        val string = leafOfType(GeblangTokenTypes.STRING)
        val tokenizer = strategy.getTokenizer(string)
        assertSame(SpellcheckingStrategy.TEXT_TOKENIZER, tokenizer)
    }

    fun testIdentifierLeafUsesWordSplittingTokenizer() {
        val identifier = leafOfType(GeblangTokenTypes.IDENTIFIER, "userAge")
        val tokenizer = strategy.getTokenizer(identifier)

        assertNotSame(SpellcheckingStrategy.EMPTY_TOKENIZER, tokenizer)
        assertInstanceOf(tokenizer, TokenizerBase::class.java)
    }

    fun testKeywordLeafIsNotSpellchecked() {
        val keyword = leafOfType(GeblangTokenTypes.KEYWORD, "func")
        val tokenizer = strategy.getTokenizer(keyword)
        assertSame(SpellcheckingStrategy.EMPTY_TOKENIZER, tokenizer)
    }

    fun testOperatorLeafIsNotSpellchecked() {
        val colon = leafOfType(GeblangTokenTypes.OPERATOR, ":")
        val tokenizer = strategy.getTokenizer(colon)
        assertSame(SpellcheckingStrategy.EMPTY_TOKENIZER, tokenizer)
    }

    fun testNumberLeafIsNotSpellchecked() {
        val file = myFixture.configureByText(GeblangFileType, "let x = 42")
        val number = file.children.first { it.node.elementType == GeblangTokenTypes.NUMBER }
        val tokenizer = strategy.getTokenizer(number)
        assertSame(SpellcheckingStrategy.EMPTY_TOKENIZER, tokenizer)
    }
}
