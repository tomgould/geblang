package com.dwgebler.geblang.templates

import junit.framework.TestCase
import org.w3c.dom.Element
import javax.xml.parsers.DocumentBuilderFactory

/**
 * Validates `liveTemplates/Geblang.xml` (the bundled live template set) directly as
 * XML on the classpath, with no running IDE / PSI / template engine involved.
 *
 * The number of `<template>` elements is asserted against the number of snippets in
 * the source-of-truth `vscode-geblang/snippets/geblang.json` file that this set was
 * ported from, so the two are guaranteed to stay in sync: 102 snippets, 102 templates.
 */
class GeblangLiveTemplatesTest : TestCase() {

    companion object {
        // Snippet count in vscode-geblang/snippets/geblang.json at the time this
        // template set was ported. Update both together if snippets are added/removed.
        const val EXPECTED_TEMPLATE_COUNT = 102
    }

    private fun loadTemplateSet(): Element {
        val resource = javaClass.classLoader.getResourceAsStream("liveTemplates/Geblang.xml")
        assertNotNull("liveTemplates/Geblang.xml not found on the test classpath", resource)
        val factory = DocumentBuilderFactory.newInstance()
        // No external DTD/network access needed or wanted for this well-formedness check.
        factory.isNamespaceAware = false
        val builder = factory.newDocumentBuilder()
        val document = resource!!.use { builder.parse(it) }
        return document.documentElement
    }

    private fun templateElements(root: Element): List<Element> {
        val nodeList = root.getElementsByTagName("template")
        return (0 until nodeList.length).map { nodeList.item(it) as Element }
    }

    fun testTemplateSetIsWellFormedXmlWithGeblangGroup() {
        val root = loadTemplateSet()
        assertEquals("templateSet", root.tagName)
        assertEquals("Geblang", root.getAttribute("group"))
    }

    fun testTemplateCountMatchesSourceSnippetCount() {
        val root = loadTemplateSet()
        val templates = templateElements(root)
        assertEquals(
            "Number of <template> elements must match the number of source " +
                "vscode-geblang snippets ($EXPECTED_TEMPLATE_COUNT)",
            EXPECTED_TEMPLATE_COUNT,
            templates.size
        )
    }

    fun testEveryTemplateHasNonBlankNameAndValue() {
        val root = loadTemplateSet()
        val templates = templateElements(root)
        assertTrue("Expected at least one template", templates.isNotEmpty())
        for (template in templates) {
            val name = template.getAttribute("name")
            val value = template.getAttribute("value")
            assertTrue("Template has a blank name: $template", name.isNotBlank())
            assertTrue("Template '$name' has a blank value", value.isNotBlank())
        }
    }

    fun testEveryTemplateCarriesTheGeblangContextOption() {
        val root = loadTemplateSet()
        val templates = templateElements(root)
        for (template in templates) {
            val name = template.getAttribute("name")
            val contextNodes = template.getElementsByTagName("context")
            assertEquals("Template '$name' must have exactly one <context>", 1, contextNodes.length)
            val context = contextNodes.item(0) as Element
            val options = context.getElementsByTagName("option")
            var foundGeblangTrue = false
            for (i in 0 until options.length) {
                val option = options.item(i) as Element
                if (option.getAttribute("name") == "GEBLANG" && option.getAttribute("value") == "true") {
                    foundGeblangTrue = true
                }
            }
            assertTrue(
                "Template '$name' must carry <option name=\"GEBLANG\" value=\"true\"/>",
                foundGeblangTrue
            )
        }
    }

    fun testTemplateNamesAreUnique() {
        val root = loadTemplateSet()
        val templates = templateElements(root)
        val names = templates.map { it.getAttribute("name") }
        assertEquals(
            "Template prefixes (names) must be unique within the group",
            names.size,
            names.toSet().size
        )
    }

    fun testNoUnresolvedVscodeSnippetSyntaxLeaksIntoTemplateValues() {
        // Guards against a broken conversion leaving raw VS Code tabstop syntax
        // (${1:foo}, $1, ${2|a,b|}) in the ported template body.
        val vscodeTabstop = Regex("""\$\{\d+[:|]|\$\d+(?!\d)""")
        val root = loadTemplateSet()
        for (template in templateElements(root)) {
            val name = template.getAttribute("name")
            val value = template.getAttribute("value")
            assertFalse(
                "Template '$name' still contains unconverted VS Code tabstop syntax: $value",
                vscodeTabstop.containsMatchIn(value)
            )
        }
    }
}
