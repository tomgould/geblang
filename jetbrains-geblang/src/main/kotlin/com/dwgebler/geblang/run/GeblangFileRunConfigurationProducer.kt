package com.dwgebler.geblang.run

import com.dwgebler.geblang.psi.GeblangFile
import com.intellij.execution.actions.ConfigurationContext
import com.intellij.execution.actions.LazyRunConfigurationProducer
import com.intellij.execution.configurations.ConfigurationFactory
import com.intellij.openapi.util.Ref
import com.intellij.psi.PsiElement

/**
 * Produces a [GeblangFileRunConfiguration] targeting the enclosing `.gb` file when the
 * context is anywhere inside a file containing a top-level `func main(` declaration
 * (see [GeblangRunAnchors.hasTopLevelMain]).
 *
 * This is the producer [GeblangRunLineMarkerContributor]'s "Run file" gutter icon
 * dispatches through via [com.intellij.execution.lineMarker.ExecutorAction]: clicking
 * Run/Debug on the `main` anchor resolves a [ConfigurationContext] from the click
 * location, and the platform asks every registered [LazyRunConfigurationProducer]
 * (this one included) whether it recognises that context.
 */
class GeblangFileRunConfigurationProducer : LazyRunConfigurationProducer<GeblangFileRunConfiguration>() {

    override fun getConfigurationFactory(): ConfigurationFactory =
        GeblangFileRunConfigurationType().configurationFactories.single()

    override fun setupConfigurationFromContext(
        configuration: GeblangFileRunConfiguration,
        context: ConfigurationContext,
        sourceElement: Ref<PsiElement>
    ): Boolean {
        val file = geblangFileWithMain(context) ?: return false
        val path = file.virtualFile?.path ?: return false

        configuration.target = path
        configuration.name = file.virtualFile?.nameWithoutExtension ?: file.name
        return true
    }

    override fun isConfigurationFromContext(
        configuration: GeblangFileRunConfiguration,
        context: ConfigurationContext
    ): Boolean {
        val file = geblangFileWithMain(context) ?: return false
        val path = file.virtualFile?.path ?: return false
        return configuration.target == path
    }

    /**
     * Resolves the context to a [GeblangFile] that declares a top-level `func main(`,
     * or `null` if the context isn't inside such a file.
     */
    private fun geblangFileWithMain(context: ConfigurationContext): GeblangFile? {
        val psiElement = context.psiLocation ?: return null
        val file = psiElement.containingFile as? GeblangFile ?: return null
        return file.takeIf { GeblangRunAnchors.hasTopLevelMain(it) }
    }
}
