package com.dwgebler.geblang.psi

import com.dwgebler.geblang.language.GeblangFileType
import com.intellij.psi.PsiErrorElement
import com.intellij.psi.util.PsiTreeUtil
import com.intellij.testFramework.fixtures.BasePlatformTestCase

/**
 * Verifies the minimal flat-token [GeblangParserDefinition] produces a valid,
 * error-free PSI tree and that the tree's text is a lossless round-trip of
 * the original source. There is no grammar to validate here - only that the
 * platform can build PSI over the lexer's token stream without ever emitting
 * a [PsiErrorElement].
 */
class GeblangParserDefinitionTest : BasePlatformTestCase() {

    private val snippet = """
        @Assert.range(1, 100)
        func setAge(int age): int {
            # validate and store the given age
            let doubled = age * 2 // 1
            let label = "age: ${'$'}{age}"
            return doubled
        }
    """.trimIndent()

    fun testParsesToGeblangFileWithNoErrorElements() {
        val file = myFixture.configureByText(GeblangFileType, snippet)
        assertInstanceOf(file, GeblangFile::class.java)

        val errors = PsiTreeUtil.findChildrenOfType(file, PsiErrorElement::class.java)
        assertTrue(
            "Expected zero PsiErrorElement nodes, found: " +
                errors.joinToString { it.errorDescription },
            errors.isEmpty()
        )
    }

    fun testPsiTextIsLosslessRoundTripOfSource() {
        val file = myFixture.configureByText(GeblangFileType, snippet)
        assertEquals(snippet, file.text)
    }

    fun testTreeIsFlatLeafSequenceMatchingLexerTokens() {
        val file = myFixture.configureByText(GeblangFileType, snippet)

        // Every direct child of the file must be a leaf (no grandchildren) -
        // this is the "flat tree" contract: one PSI node per lexer token,
        // no nesting, no grammar rules applied.
        val children = file.children
        assertTrue("Expected at least one PSI child", children.isNotEmpty())
        for (child in children) {
            assertEquals(
                "Expected a flat leaf node but '${child.text}' has children",
                0,
                child.children.size
            )
        }

        // Concatenating the flat children's text must reconstruct the source
        // exactly (matches the lexer's own gap/overlap-free guarantee).
        val rebuilt = children.joinToString("") { it.text }
        assertEquals(snippet, rebuilt)
    }
}
