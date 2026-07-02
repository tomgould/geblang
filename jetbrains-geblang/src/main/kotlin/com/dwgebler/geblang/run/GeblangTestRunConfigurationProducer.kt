package com.dwgebler.geblang.run

import com.dwgebler.geblang.psi.GeblangFile
import com.intellij.execution.actions.ConfigurationContext
import com.intellij.execution.actions.LazyRunConfigurationProducer
import com.intellij.execution.configurations.ConfigurationFactory
import com.intellij.openapi.util.Ref
import com.intellij.psi.PsiElement

/**
 * Produces a [GeblangTestRunConfiguration] targeting the enclosing `.gb` file when the
 * context is anywhere inside a file that declares a `class X extends test.Test` or an
 * `@test`-decorated method (see [GeblangRunAnchors.isTestClassAnchor] /
 * [GeblangRunAnchors.isTestMethodAnchor]).
 *
 * This is the producer [GeblangRunLineMarkerContributor]'s "Run test class"/"Run test
 * method" gutter icons dispatch through via
 * [com.intellij.execution.lineMarker.ExecutorAction]: clicking Run/Debug on either
 * test anchor resolves a [ConfigurationContext] from the click location, and the
 * platform asks every registered [LazyRunConfigurationProducer] (this one included)
 * whether it recognises that context. Reuses the existing
 * [GeblangTestRunConfiguration] - it already runs `geblang test --format teamcity
 * <target>` against a file (or directory) and renders results in the native test
 * tree, which is exactly what a test-class/test-method gutter anchor should do.
 */
class GeblangTestRunConfigurationProducer : LazyRunConfigurationProducer<GeblangTestRunConfiguration>() {

    override fun getConfigurationFactory(): ConfigurationFactory =
        GeblangTestRunConfigurationType().configurationFactories.single()

    override fun setupConfigurationFromContext(
        configuration: GeblangTestRunConfiguration,
        context: ConfigurationContext,
        sourceElement: Ref<PsiElement>
    ): Boolean {
        val file = geblangTestFile(context) ?: return false
        val path = file.virtualFile?.path ?: return false

        configuration.target = path
        configuration.name = file.virtualFile?.nameWithoutExtension ?: file.name
        return true
    }

    override fun isConfigurationFromContext(
        configuration: GeblangTestRunConfiguration,
        context: ConfigurationContext
    ): Boolean {
        val file = geblangTestFile(context) ?: return false
        val path = file.virtualFile?.path ?: return false
        return configuration.target == path
    }

    /**
     * Resolves the context to a [GeblangFile] that declares a test class or an
     * `@test` method, or `null` if the context isn't inside such a file.
     */
    private fun geblangTestFile(context: ConfigurationContext): GeblangFile? {
        val psiElement = context.psiLocation ?: return null
        val file = psiElement.containingFile as? GeblangFile ?: return null
        return file.takeIf { GeblangRunAnchors.hasTestAnchor(it) }
    }
}
