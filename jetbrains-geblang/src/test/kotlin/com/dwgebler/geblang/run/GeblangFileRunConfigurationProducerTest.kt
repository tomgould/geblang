package com.dwgebler.geblang.run

import com.dwgebler.geblang.language.GeblangFileType
import com.intellij.execution.actions.ConfigurationContext
import com.intellij.testFramework.fixtures.BasePlatformTestCase

/**
 * Verifies [GeblangFileRunConfigurationProducer] against a real (but headless)
 * [ConfigurationContext] built from a [com.intellij.psi.PsiFile] fixture -
 * [ConfigurationContext] has a public single-[com.intellij.psi.PsiElement]
 * constructor, so this does not require a running IDE or a live execution
 * environment. [com.intellij.execution.actions.RunConfigurationProducer.setupConfigurationFromContext]
 * itself is `protected`, so it is exercised indirectly through the public
 * [com.intellij.execution.actions.RunConfigurationProducer.createConfigurationFromContext]
 * entry point, which is the same path the platform uses when dispatching a gutter
 * Run/Debug click.
 */
class GeblangFileRunConfigurationProducerTest : BasePlatformTestCase() {

    private val mainSnippet = """
        func main(): void {
        }
    """.trimIndent()

    private val noMainSnippet = """
        func helper(): void {
        }
    """.trimIndent()

    fun testCreateConfigurationSucceedsForFileWithMain() {
        val file = myFixture.configureByText(GeblangFileType, mainSnippet)
        val context = ConfigurationContext(file)
        val producer = GeblangFileRunConfigurationProducer()

        val fromContext = producer.createConfigurationFromContext(context)

        assertNotNull("Expected a configuration for a file containing func main(", fromContext)
        val configuration = fromContext!!.configuration as GeblangFileRunConfiguration
        assertEquals(file.virtualFile.path, configuration.target)
    }

    fun testCreateConfigurationFailsForFileWithoutMain() {
        val file = myFixture.configureByText("no_main.gb", noMainSnippet)
        val context = ConfigurationContext(file)
        val producer = GeblangFileRunConfigurationProducer()

        val fromContext = producer.createConfigurationFromContext(context)

        assertNull("Expected no configuration for a file with no func main()", fromContext)
    }

    fun testIsConfigurationFromContextMatchesAfterSetup() {
        val file = myFixture.configureByText(GeblangFileType, mainSnippet)
        val context = ConfigurationContext(file)
        val producer = GeblangFileRunConfigurationProducer()

        val fromContext = producer.createConfigurationFromContext(context)
        val configuration = fromContext!!.configuration as GeblangFileRunConfiguration

        assertTrue(producer.isConfigurationFromContext(configuration, context))
    }

    fun testIsConfigurationFromContextFalseForDifferentTarget() {
        val file = myFixture.configureByText(GeblangFileType, mainSnippet)
        val context = ConfigurationContext(file)
        val producer = GeblangFileRunConfigurationProducer()
        val factory = GeblangFileRunConfigurationType().configurationFactories.single()
        val configuration = GeblangFileRunConfiguration(project, factory, "Geblang File")
        configuration.target = "/some/other/file.gb"

        assertFalse(producer.isConfigurationFromContext(configuration, context))
    }
}
