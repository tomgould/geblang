package com.dwgebler.geblang.highlighting

import com.intellij.lexer.Lexer
import com.intellij.testFramework.LexerTestCase

/**
 * Unit tests for [GeblangLexer].
 *
 * These tests exercise the hand-written lexer directly (no PSI/parser involved).
 * Expected token dumps use the format produced by [LexerTestCase.printTokens]:
 * one line per token, `TOKEN_TYPE ('token text')`, with embedded newlines in the
 * token text rendered as the literal two characters `\n`.
 *
 * The most important test in this file is [testIntegerDivisionIsNotAComment],
 * which locks in the language's defining lexical quirk: `#` starts a line
 * comment, but `//` is the integer-division operator and must never be
 * swallowed as a comment.
 */
class GeblangLexerTest : LexerTestCase() {

    override fun createLexer(): Lexer = GeblangLexer()

    override fun getDirPath(): String = ""

    // ------------------------------------------------------------------
    // Comments
    // ------------------------------------------------------------------

    fun testLineComment() {
        doTest(
            "# comment",
            "LINE_COMMENT ('# comment')\n"
        )
    }

    fun testDocLineComment() {
        doTest(
            "## doc",
            "LINE_COMMENT ('## doc')\n"
        )
    }

    fun testBlockComment() {
        doTest(
            "/* block */",
            "BLOCK_COMMENT ('/* block */')\n"
        )
    }

    fun testDocBlockComment() {
        doTest(
            "/** doc block */",
            "BLOCK_COMMENT ('/** doc block */')\n"
        )
    }

    // ------------------------------------------------------------------
    // The critical guard case: // is integer division, NOT a comment
    // ------------------------------------------------------------------

    fun testIntegerDivisionIsNotAComment() {
        doTest(
            "//",
            "OPERATOR ('//')\n"
        )
    }

    fun testIntegerDivisionExpression() {
        doTest(
            "10 // 3",
            "NUMBER ('10')\n" +
                "WHITESPACE (' ')\n" +
                "OPERATOR ('//')\n" +
                "WHITESPACE (' ')\n" +
                "NUMBER ('3')\n"
        )
    }

    // ------------------------------------------------------------------
    // Strings
    // ------------------------------------------------------------------

    fun testDoubleQuotedString() {
        doTest(
            "\"hello\"",
            "STRING ('\"hello\"')\n"
        )
    }

    fun testTripleDoubleQuotedString() {
        doTest(
            "\"\"\"hello\"\"\"",
            "STRING ('\"\"\"hello\"\"\"')\n"
        )
    }

    fun testSingleQuotedString() {
        doTest(
            "'hello'",
            "STRING (''hello'')\n"
        )
    }

    fun testTripleSingleQuotedString() {
        doTest(
            "'''hello'''",
            "STRING (''''hello'''')\n"
        )
    }

    fun testDoubleQuotedStringWithInterpolation() {
        // The lexer's double-quoted-string scanner does not special-case `${...}` —
        // it simply scans forward until the closing `"` (honouring backslash escapes),
        // so the whole literal including the interpolation braces is emitted as a
        // single STRING token. GeblangTokenTypes.INTERPOLATION is never produced.
        doTest(
            "\"value: \${expr}\"",
            "STRING ('\"value: \${expr}\"')\n"
        )
    }

    fun testDoubleQuotedStringWithEscapes() {
        doTest(
            "\"line1\\nline2\"",
            "STRING ('\"line1\\nline2\"')\n"
        )
    }

    // ------------------------------------------------------------------
    // Numbers
    // ------------------------------------------------------------------

    fun testDecimalNumber() {
        doTest(
            "42",
            "NUMBER ('42')\n"
        )
    }

    fun testUnderscoreSeparatedNumber() {
        doTest(
            "1_000",
            "NUMBER ('1_000')\n"
        )
    }

    fun testFloatNumber() {
        doTest(
            "3.14",
            "NUMBER ('3.14')\n"
        )
    }

    fun testFloatWithSuffix() {
        doTest(
            "3.14f",
            "NUMBER ('3.14f')\n"
        )
    }

    fun testScientificNotation() {
        doTest(
            "1.5e-3",
            "NUMBER ('1.5e-3')\n"
        )
    }

    fun testHexNumber() {
        doTest(
            "0xFF",
            "NUMBER ('0xFF')\n"
        )
    }

    fun testOctalNumber() {
        doTest(
            "0o755",
            "NUMBER ('0o755')\n"
        )
    }

    fun testBinaryNumber() {
        doTest(
            "0b1010",
            "NUMBER ('0b1010')\n"
        )
    }

    // ------------------------------------------------------------------
    // Keywords
    // ------------------------------------------------------------------

    fun testControlFlowKeywords() {
        doTest(
            "if for match return",
            "KEYWORD ('if')\n" +
                "WHITESPACE (' ')\n" +
                "KEYWORD ('for')\n" +
                "WHITESPACE (' ')\n" +
                "KEYWORD ('match')\n" +
                "WHITESPACE (' ')\n" +
                "KEYWORD ('return')\n"
        )
    }

    fun testDeclarationKeywords() {
        doTest(
            "func class let const",
            "KEYWORD ('func')\n" +
                "WHITESPACE (' ')\n" +
                "KEYWORD ('class')\n" +
                "WHITESPACE (' ')\n" +
                "KEYWORD ('let')\n" +
                "WHITESPACE (' ')\n" +
                "KEYWORD ('const')\n"
        )
    }

    // ------------------------------------------------------------------
    // Constants
    // ------------------------------------------------------------------

    fun testConstants() {
        doTest(
            "true false null this",
            "CONSTANT ('true')\n" +
                "WHITESPACE (' ')\n" +
                "CONSTANT ('false')\n" +
                "WHITESPACE (' ')\n" +
                "CONSTANT ('null')\n" +
                "WHITESPACE (' ')\n" +
                "CONSTANT ('this')\n"
        )
    }

    // ------------------------------------------------------------------
    // Word operators
    // ------------------------------------------------------------------

    fun testWordOperators() {
        doTest(
            "is not xor",
            "WORD_OPERATOR ('is')\n" +
                "WHITESPACE (' ')\n" +
                "WORD_OPERATOR ('not')\n" +
                "WHITESPACE (' ')\n" +
                "WORD_OPERATOR ('xor')\n"
        )
    }

    // ------------------------------------------------------------------
    // Types
    // ------------------------------------------------------------------

    fun testBuiltinTypes() {
        doTest(
            "int decimal string bool list dict set range",
            "TYPE ('int')\n" +
                "WHITESPACE (' ')\n" +
                "TYPE ('decimal')\n" +
                "WHITESPACE (' ')\n" +
                "TYPE ('string')\n" +
                "WHITESPACE (' ')\n" +
                "TYPE ('bool')\n" +
                "WHITESPACE (' ')\n" +
                "TYPE ('list')\n" +
                "WHITESPACE (' ')\n" +
                "TYPE ('dict')\n" +
                "WHITESPACE (' ')\n" +
                "TYPE ('set')\n" +
                "WHITESPACE (' ')\n" +
                "TYPE ('range')\n"
        )
    }

    // ------------------------------------------------------------------
    // Multi-character operators
    // ------------------------------------------------------------------

    fun testMultiCharOperators() {
        val ops = listOf("//", "**", "??=", "?.", "|>", "..", "+=", "==", "=>")
        val text = ops.joinToString(" ")
        val expected = StringBuilder()
        for ((i, op) in ops.withIndex()) {
            expected.append("OPERATOR ('").append(op).append("')\n")
            if (i != ops.lastIndex) expected.append("WHITESPACE (' ')\n")
        }
        doTest(text, expected.toString())
    }

    // ------------------------------------------------------------------
    // Brackets
    // ------------------------------------------------------------------

    fun testBrackets() {
        doTest(
            "{}[]()",
            "LBRACE ('{')\n" +
                "RBRACE ('}')\n" +
                "LBRACKET ('[')\n" +
                "RBRACKET (']')\n" +
                "LPAREN ('(')\n" +
                "RPAREN (')')\n"
        )
    }

    // ------------------------------------------------------------------
    // Realistic multi-line snippet
    // ------------------------------------------------------------------

    fun testRealisticSnippet() {
        val text = """
            func add(int a, int b): int {
                # sum the two operands
                return a + b // 1
            }
        """.trimIndent()

        doTest(
            text,
            "KEYWORD ('func')\n" +
                "WHITESPACE (' ')\n" +
                "IDENTIFIER ('add')\n" +
                "LPAREN ('(')\n" +
                "TYPE ('int')\n" +
                "WHITESPACE (' ')\n" +
                "IDENTIFIER ('a')\n" +
                "OPERATOR (',')\n" +
                "WHITESPACE (' ')\n" +
                "TYPE ('int')\n" +
                "WHITESPACE (' ')\n" +
                "IDENTIFIER ('b')\n" +
                "RPAREN (')')\n" +
                "OPERATOR (':')\n" +
                "WHITESPACE (' ')\n" +
                "TYPE ('int')\n" +
                "WHITESPACE (' ')\n" +
                "LBRACE ('{')\n" +
                "WHITESPACE ('\\n    ')\n" +
                "LINE_COMMENT ('# sum the two operands')\n" +
                "WHITESPACE ('\\n    ')\n" +
                "KEYWORD ('return')\n" +
                "WHITESPACE (' ')\n" +
                "IDENTIFIER ('a')\n" +
                "WHITESPACE (' ')\n" +
                "OPERATOR ('+')\n" +
                "WHITESPACE (' ')\n" +
                "IDENTIFIER ('b')\n" +
                "WHITESPACE (' ')\n" +
                "OPERATOR ('//')\n" +
                "WHITESPACE (' ')\n" +
                "NUMBER ('1')\n" +
                "WHITESPACE ('\\n')\n" +
                "RBRACE ('}')\n"
        )
    }

    // ------------------------------------------------------------------
    // Bad character handling
    // ------------------------------------------------------------------

    fun testBadCharacter() {
        // '`' (backtick) is not matched by any branch of GeblangLexer.nextToken():
        // not whitespace, not #, not /* , not a quote, not a digit, not a letter/underscore,
        // not a bracket, and not present in ONE_CHAR_OPS/TWO_CHAR_OPS/THREE_CHAR_OPS.
        doTest(
            "`",
            "BAD_CHARACTER ('`')\n"
        )
    }

    // ------------------------------------------------------------------
    // Round-trip / full coverage sanity check
    // ------------------------------------------------------------------

    fun testRoundTripCoversEntireInputWithNoGapsOrOverlaps() {
        val text = """
            func add(int a, int b): int {
                # sum the two operands
                let result = a + b // 1
                return result
            }
        """.trimIndent()

        val lexer = createLexer()
        lexer.start(text, 0, text.length, 0)

        val rebuilt = StringBuilder()
        var previousEnd = 0
        while (lexer.tokenType != null) {
            assertEquals(
                "Gap or overlap detected before token '${lexer.tokenText}' at offset ${lexer.tokenStart}",
                previousEnd,
                lexer.tokenStart
            )
            rebuilt.append(lexer.tokenText)
            previousEnd = lexer.tokenEnd
            lexer.advance()
        }

        assertEquals("Lexer did not consume the entire buffer", text.length, previousEnd)
        assertEquals(text.length, lexer.bufferEnd)
        assertEquals("Concatenated token text must equal the original input", text, rebuilt.toString())
    }

    fun testCorrectRestart() {
        val text = "func add(int a, int b): int {\n    return a + b // 1\n}"
        checkCorrectRestart(text)
    }
}
