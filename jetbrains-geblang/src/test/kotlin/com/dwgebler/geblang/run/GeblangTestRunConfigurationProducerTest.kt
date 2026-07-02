package com.dwgebler.geblang.run

import com.dwgebler.geblang.language.GeblangFileType
import com.intellij.execution.actions.ConfigurationContext
import com.intellij.testFramework.fixtures.BasePlatformTestCase

/**
 * Verifies [GeblangTestRunConfigurationProducer] against a real (but headless)
 * [ConfigurationContext] built from a [com.intellij.psi.PsiFile] fixture, matching
 * the approach in [GeblangFileRunConfigurationProducerTest] (exercised through the
 * public [com.intellij.execution.actions.RunConfigurationProducer.createConfigurationFromContext]
 * entry point since [com.intellij.execution.actions.RunConfigurationProducer.setupConfigurationFromContext]
 * itself is `protected`).
 */
class GeblangTestRunConfigurationProducerTest : BasePlatformTestCase() {

    private val testClassSnippet = """
        import test;

        class FooTest extends test.Test {
            @test
            func testX(): void {
                this.assertEquals(true, true)
            }
        }
    """.trimIndent()

    private val noTestSnippet = """
        func helper(): void {
        }
    """.trimIndent()

    fun testCreateConfigurationSucceedsForFileWithTestClass() {
        val file = myFixture.configureByText(GeblangFileType, testClassSnippet)
        val context = ConfigurationContext(file)
        val producer = GeblangTestRunConfigurationProducer()

        val fromContext = producer.createConfigurationFromContext(context)

        assertNotNull("Expected a configuration for a file containing a test class", fromContext)
        val configuration = fromContext!!.configuration as GeblangTestRunConfiguration
        assertEquals(file.virtualFile.path, configuration.target)
    }

    fun testCreateConfigurationFailsForFileWithoutTestAnchor() {
        val file = myFixture.configureByText("no_tests.gb", noTestSnippet)
        val context = ConfigurationContext(file)
        val producer = GeblangTestRunConfigurationProducer()

        val fromContext = producer.createConfigurationFromContext(context)

        assertNull("Expected no configuration for a file with no test class or @test method", fromContext)
    }

    fun testIsConfigurationFromContextMatchesAfterSetup() {
        val file = myFixture.configureByText(GeblangFileType, testClassSnippet)
        val context = ConfigurationContext(file)
        val producer = GeblangTestRunConfigurationProducer()

        val fromContext = producer.createConfigurationFromContext(context)
        val configuration = fromContext!!.configuration as GeblangTestRunConfiguration

        assertTrue(producer.isConfigurationFromContext(configuration, context))
    }

    fun testIsConfigurationFromContextFalseForDifferentTarget() {
        val file = myFixture.configureByText(GeblangFileType, testClassSnippet)
        val context = ConfigurationContext(file)
        val producer = GeblangTestRunConfigurationProducer()
        val factory = GeblangTestRunConfigurationType().configurationFactories.single()
        val configuration = GeblangTestRunConfiguration(project, factory, "Geblang Test")
        configuration.target = "/some/other/file.gb"

        assertFalse(producer.isConfigurationFromContext(configuration, context))
    }
}
