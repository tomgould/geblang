package com.dwgebler.geblang.lsp

import com.dwgebler.geblang.settings.GeblangSettings
import com.intellij.openapi.project.Project
import com.redhat.devtools.lsp4ij.LanguageServerFactory
import com.redhat.devtools.lsp4ij.server.StreamConnectionProvider

/**
 * Factory that creates the Geblang LSP connection provider.
 * Registered in plugin.xml under the lsp4ij `server` extension point.
 *
 * The LSP server is launched by running `geblang lsp` (stdio mode).
 * The path to the geblang binary is read from GeblangSettings and defaults to "geblang"
 * (i.e., resolved on PATH).
 */
class GeblangLspServerFactory : LanguageServerFactory {

    override fun createConnectionProvider(project: Project): StreamConnectionProvider {
        val execPath = GeblangSettings.getInstance().geblangExecutablePath.ifBlank { "geblang" }
        return GeblangStreamConnectionProvider(execPath)
    }
}
