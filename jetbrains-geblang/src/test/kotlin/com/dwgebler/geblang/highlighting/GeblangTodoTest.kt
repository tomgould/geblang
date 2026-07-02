package com.dwgebler.geblang.highlighting

import com.dwgebler.geblang.language.GeblangFileType
import com.intellij.psi.search.PsiTodoSearchHelper
import com.intellij.testFramework.fixtures.BasePlatformTestCase

/**
 * Verifies that TODO items in Geblang comments are found by the platform's
 * TODO indexing (the "TODO" tool window and the `# TODO:` gutter/tree
 * feature). This is the test that determines whether Part-1 needs a
 * [com.intellij.lang.cacheBuilder.WordsScanner] registration: TODO indexing
 * only scans languages whose words are exposed to `IdIndex`/`TodoIndex` via
 * a words scanner, so a flat-PSI language with no such registration may find
 * nothing here by default.
 */
class GeblangTodoTest : BasePlatformTestCase() {

    private val snippet = """
        # TODO: wire this up
        func placeholder(): void {
            /* FIXME: later */
            let ignored = 0
        }
    """.trimIndent()

    fun testTodoItemsAreFoundInLineAndBlockComments() {
        val file = myFixture.configureByText(GeblangFileType, snippet)

        val todoItems = PsiTodoSearchHelper.getInstance(project).findTodoItems(file)

        assertEquals(
            "Expected exactly 2 TODO items (TODO + FIXME), found: ${todoItems.size}",
            2,
            todoItems.size
        )

        val texts = todoItems.map { item -> file.text.substring(item.textRange.startOffset, item.textRange.endOffset) }
        assertTrue(
            "Expected a TODO item covering the '# TODO: wire this up' comment, found: $texts",
            texts.any { it.contains("TODO: wire this up") }
        )
        assertTrue(
            "Expected a TODO item covering the '/* FIXME: later */' comment, found: $texts",
            texts.any { it.contains("FIXME: later") }
        )
    }

    fun testFilesWithTodoItemsAreDiscoverable() {
        myFixture.configureByText(GeblangFileType, snippet)

        var sawGeblangFile = false
        PsiTodoSearchHelper.getInstance(project).processFilesWithTodoItems { psiFile ->
            if (psiFile.name.endsWith(".gb")) sawGeblangFile = true
            true
        }

        assertTrue(
            "Expected the configured .gb file to be reported as containing TODO items",
            sawGeblangFile
        )
    }
}
