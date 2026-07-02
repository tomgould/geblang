package com.dwgebler.geblang.dap

import com.dwgebler.geblang.settings.GeblangSettings
import com.intellij.execution.configurations.RunConfigurationOptions
import com.intellij.execution.runners.ExecutionEnvironment
import com.redhat.devtools.lsp4ij.dap.descriptors.DebugAdapterDescriptor
import com.redhat.devtools.lsp4ij.dap.descriptors.DebugAdapterDescriptorFactory

/**
 * Factory that creates the Geblang debug adapter descriptor.
 * Registered in plugin.xml under the lsp4ij `debugAdapterServer` extension point.
 *
 * The DAP server is launched by running `geblang dap` (stdio transport, same
 * subprocess-over-stdio model as [com.dwgebler.geblang.lsp.GeblangLspServerFactory]
 * launching `geblang lsp`). The path to the geblang binary is read from
 * [GeblangSettings] and defaults to "geblang" (i.e., resolved on PATH).
 */
class GeblangDebugAdapterDescriptorFactory : DebugAdapterDescriptorFactory() {

    override fun createDebugAdapterDescriptor(
        options: RunConfigurationOptions,
        environment: ExecutionEnvironment
    ): DebugAdapterDescriptor {
        val execPath = GeblangSettings.getInstance().geblangExecutablePath.ifBlank { "geblang" }
        return GeblangDebugAdapterDescriptor(options, environment, serverDefinition, execPath)
    }
}
