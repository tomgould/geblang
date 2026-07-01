package com.dwgebler.geblang.actions

import junit.framework.TestCase

/**
 * Validates the bundled "New > Geblang File" templates directly as classpath
 * resources, with no running IDE / template engine involved.
 *
 * Each `.ft` file under `fileTemplates/internal/` is loaded as plain text and
 * checked for the Geblang keywords its content is expected to contain. The
 * actual Velocity `${NAME}` substitution and IDE-side wizard flow are only
 * exercised manually via `runIde` (see README "New file templates" section).
 */
class GeblangFileTemplatesTest : TestCase() {

    private fun loadTemplate(fileName: String): String {
        val path = "fileTemplates/internal/$fileName"
        val resource = javaClass.classLoader.getResourceAsStream(path)
        assertNotNull("$path not found on the test classpath", resource)
        return resource!!.use { it.readBytes().toString(Charsets.UTF_8) }
    }

    fun testFileTemplateIsNonEmptyAndUsesNameVariable() {
        val content = loadTemplate("Geblang File.gb.ft")
        assertTrue("template should not be blank", content.isNotBlank())
        assertTrue("expected \${NAME} placeholder", content.contains("\${NAME}"))
        assertTrue("expected a func declaration", content.contains("func main(): void"))
    }

    fun testClassTemplateContainsClassKeywordAndConstructor() {
        val content = loadTemplate("Geblang Class.gb.ft")
        assertTrue("template should not be blank", content.isNotBlank())
        assertTrue("expected \${NAME} placeholder", content.contains("\${NAME}"))
        assertTrue("expected a class declaration", content.contains("class \${NAME}"))
        assertTrue("expected a constructor named after the class", content.contains("func \${NAME}("))
    }

    fun testModuleTemplateContainsModuleDeclarationAndExportedFunction() {
        val content = loadTemplate("Geblang Module.gb.ft")
        assertTrue("template should not be blank", content.isNotBlank())
        assertTrue("expected \${NAME} placeholder", content.contains("\${NAME}"))
        assertTrue("expected a module declaration", content.contains("module \${NAME};"))
        assertTrue("expected an exported function", content.contains("export func"))
    }

    fun testTestTemplateContainsTestClassAndTestDecorator() {
        val content = loadTemplate("Geblang Test.gb.ft")
        assertTrue("template should not be blank", content.isNotBlank())
        assertTrue("expected the test module import", content.contains("import test;"))
        assertTrue("expected the class to extend test.Test", content.contains("extends test.Test"))
        assertTrue("expected an @test-decorated method", content.contains("@test"))
    }

    fun testNoTemplateUsesUnconvertedVsCodeTabstopSyntax() {
        val fileNames = listOf(
            "Geblang File.gb.ft",
            "Geblang Class.gb.ft",
            "Geblang Module.gb.ft",
            "Geblang Test.gb.ft",
        )
        for (fileName in fileNames) {
            val content = loadTemplate(fileName)
            assertFalse("$fileName should not contain VS Code tabstop syntax", content.contains("\${1"))
        }
    }

    fun testAllTemplatesAreAsciiOnly() {
        val fileNames = listOf(
            "Geblang File.gb.ft",
            "Geblang Class.gb.ft",
            "Geblang Module.gb.ft",
            "Geblang Test.gb.ft",
        )
        for (fileName in fileNames) {
            val content = loadTemplate(fileName)
            for (ch in content) {
                assertTrue("$fileName contains a non-ASCII character: $ch", ch.code < 128)
            }
        }
    }
}
