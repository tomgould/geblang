package com.dwgebler.geblang.run

import com.dwgebler.geblang.language.GeblangFileType
import com.intellij.testFramework.fixtures.BasePlatformTestCase

/**
 * Verifies [GeblangRunLineMarkerContributor] against the flat-leaf Geblang PSI (see
 * [com.dwgebler.geblang.psi.GeblangParserDefinition]): a top-level `func main(`
 * anchor, a `class X extends test.Test` anchor, and an `@test`-decorated method
 * anchor each produce exactly one non-null [com.intellij.execution.lineMarker.RunLineMarkerContributor.Info],
 * anchored at exactly one leaf, and every other leaf in the file produces `null`.
 *
 * Only anchor *position* is asserted (i.e. which leaf, identified by its text and
 * offset, produces an Info) - icon identity and tooltip text are not asserted, since
 * they are harder to compare meaningfully and are not part of the anchor-detection
 * contract this class owns.
 */
class GeblangRunLineMarkerContributorTest : BasePlatformTestCase() {

    private val snippet = """
        import test;

        func main(): void {
            let x = 1
        }

        class FooTest extends test.Test {
            @test
            func testX(): void {
                this.assertEquals(true, true)
            }
        }
    """.trimIndent()

    private fun leaves() = myFixture.configureByText(GeblangFileType, snippet).children

    private fun infoAt(text: String) =
        leaves().filter { it.text == text }.map { GeblangRunLineMarkerContributor().getInfo(it) }

    fun testMainIdentifierLeafProducesInfo() {
        val infos = infoAt("main")
        assertEquals(1, infos.size)
        assertNotNull("Expected a non-null Info at the 'main' identifier leaf", infos.single())
    }

    fun testTestClassNameLeafProducesInfo() {
        val infos = infoAt("FooTest")
        assertEquals(1, infos.size)
        assertNotNull("Expected a non-null Info at the 'FooTest' class-name leaf", infos.single())
    }

    fun testTestMethodNameLeafProducesInfo() {
        val infos = infoAt("testX")
        assertEquals(1, infos.size)
        assertNotNull("Expected a non-null Info at the 'testX' method-name leaf", infos.single())
    }

    fun testUnrelatedIdentifierLeafProducesNoInfo() {
        // 'x' is an ordinary local variable name - not a run/debug anchor.
        val infos = infoAt("x")
        assertEquals(1, infos.size)
        assertNull("Expected no Info at an unrelated identifier leaf", infos.single())
    }

    fun testFuncKeywordLeafProducesNoInfo() {
        val contributor = GeblangRunLineMarkerContributor()
        val funcLeaves = leaves().filter { it.text == "func" }
        assertTrue("Expected at least one 'func' keyword leaf", funcLeaves.isNotEmpty())
        for (leaf in funcLeaves) {
            assertNull("Expected no Info on the 'func' keyword itself", contributor.getInfo(leaf))
        }
    }

    fun testBraceLeavesProduceNoInfo() {
        val contributor = GeblangRunLineMarkerContributor()
        val braceLeaves = leaves().filter { it.text == "{" || it.text == "}" }
        assertTrue("Expected brace leaves in the snippet", braceLeaves.isNotEmpty())
        for (leaf in braceLeaves) {
            assertNull("Expected no Info on a brace leaf", contributor.getInfo(leaf))
        }
    }

    fun testDecoratorLeafItselfProducesNoInfo() {
        // The @test decorator leaf is the trigger for the method anchor, but the
        // anchor itself must be the method-name identifier, not the decorator.
        val infos = infoAt("@test")
        assertEquals(1, infos.size)
        assertNull("Expected no Info on the '@test' decorator leaf itself", infos.single())
    }

    fun testExtendsKeywordLeafProducesNoInfo() {
        val infos = infoAt("extends")
        assertEquals(1, infos.size)
        assertNull("Expected no Info on the 'extends' keyword leaf", infos.single())
    }

    fun testExactlyThreeAnchorsInWholeFile() {
        val contributor = GeblangRunLineMarkerContributor()
        val anchors = leaves().filter { contributor.getInfo(it) != null }
        assertEquals(
            "Expected exactly one anchor each for main/testclass/testmethod, found: " +
                anchors.joinToString { it.text },
            3,
            anchors.size
        )
    }
}
