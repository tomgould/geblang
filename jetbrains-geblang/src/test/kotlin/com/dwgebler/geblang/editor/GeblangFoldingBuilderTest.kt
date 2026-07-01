package com.dwgebler.geblang.editor

import com.dwgebler.geblang.language.GeblangFileType
import com.intellij.lang.folding.FoldingDescriptor
import com.intellij.testFramework.fixtures.BasePlatformTestCase

/**
 * Verifies [GeblangFoldingBuilder] against the flat-leaf Geblang PSI (see
 * [com.dwgebler.geblang.psi.GeblangParserDefinition]): multi-line `{ ... }`
 * blocks and multi-line `/* ... */` comments fold, nested blocks produce
 * their own descriptor, and single-line `{}` never folds.
 */
class GeblangFoldingBuilderTest : BasePlatformTestCase() {

    private val snippet = """
        /*
         * Doubles a non-negative value, returning zero otherwise.
         */
        func double(int a): int {
            if (a > 0) {
                return a * 2
            }
            let ignored = {}
            return 0
        }
    """.trimIndent()

    private fun buildDescriptors(): Array<FoldingDescriptor> {
        val file = myFixture.configureByText(GeblangFileType, snippet)
        val document = myFixture.getDocument(file)
        return GeblangFoldingBuilder().buildFoldRegions(file, document, false)
    }

    fun testFuncBodyFolds() {
        val descriptors = buildDescriptors()
        val funcBodyStart = snippet.indexOf("int {") + "int ".length
        val funcBodyEnd = snippet.lastIndexOf("}") + 1

        val match = descriptors.find { it.range.startOffset == funcBodyStart }
        assertNotNull("Expected a fold region starting at the func body '{'", match)
        assertEquals(funcBodyEnd, match!!.range.endOffset)
        assertEquals("{...}", GeblangFoldingBuilder().getPlaceholderText(match.element))
    }

    fun testNestedIfBlockFolds() {
        val descriptors = buildDescriptors()
        val ifBraceStart = snippet.indexOf("(a > 0) {") + "(a > 0) ".length
        val ifBraceEnd = snippet.indexOf("}", ifBraceStart) + 1

        val match = descriptors.find { it.range.startOffset == ifBraceStart }
        assertNotNull("Expected a fold region starting at the nested if-block '{'", match)
        assertEquals(ifBraceEnd, match!!.range.endOffset)
    }

    fun testBlockCommentFolds() {
        val descriptors = buildDescriptors()
        val commentStart = snippet.indexOf("/*")
        val commentEnd = snippet.indexOf("*/") + 2

        val match = descriptors.find { it.range.startOffset == commentStart }
        assertNotNull("Expected a fold region over the multi-line block comment", match)
        assertEquals(commentEnd, match!!.range.endOffset)
        assertEquals("/*...*/", GeblangFoldingBuilder().getPlaceholderText(match.element))
    }

    fun testSingleLineBracesDoNotFold() {
        val descriptors = buildDescriptors()
        val emptyBraceStart = snippet.indexOf("{}")

        val match = descriptors.find { it.range.startOffset == emptyBraceStart }
        assertNull("Single-line '{}' must not produce a fold region", match)
    }

    fun testExactFoldCount() {
        // func body, nested if-block, block comment - and nothing else
        // (the single-line `{}` must be excluded).
        val descriptors = buildDescriptors()
        assertEquals(3, descriptors.size)
    }

    fun testIsNotCollapsedByDefault() {
        val descriptors = buildDescriptors()
        val builder = GeblangFoldingBuilder()
        for (descriptor in descriptors) {
            assertFalse(builder.isCollapsedByDefault(descriptor.element))
        }
    }
}
