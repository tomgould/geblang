package com.dwgebler.geblang.dap

import com.dwgebler.geblang.language.GeblangFileType
import com.intellij.execution.configurations.GeneralCommandLine
import com.intellij.execution.configurations.RunConfigurationOptions
import com.intellij.execution.process.ProcessHandler
import com.intellij.execution.runners.ExecutionEnvironment
import com.intellij.openapi.fileTypes.FileType
import com.redhat.devtools.lsp4ij.dap.definitions.DebugAdapterServerDefinition
import com.redhat.devtools.lsp4ij.dap.descriptors.DebugAdapterDescriptor

/**
 * Launches `<geblang> dap` as a child process and speaks the Debug Adapter Protocol
 * with it over stdio, the debug counterpart of
 * [com.dwgebler.geblang.lsp.GeblangStreamConnectionProvider] launching `geblang lsp`.
 *
 * Unlike [com.redhat.devtools.lsp4ij.dap.descriptors.DefaultDebugAdapterDescriptor],
 * which builds its command line from a user-typed [com.redhat.devtools.lsp4ij.dap.configurations.DAPRunConfigurationOptions.getCommand]
 * string (the "user-defined debug adapter" flow), this descriptor is
 * extension-point-registered and launches the resolved `geblang` binary directly -
 * there is no free-text command for a user to configure, only the executable path
 * from [com.dwgebler.geblang.settings.GeblangSettings].
 *
 * @param geblangPath the resolved path (or bare name) to the geblang executable
 */
class GeblangDebugAdapterDescriptor(
    options: RunConfigurationOptions,
    environment: ExecutionEnvironment,
    serverDefinition: DebugAdapterServerDefinition,
    private val geblangPath: String
) : DebugAdapterDescriptor(options, environment, serverDefinition) {

    override fun startServer(): ProcessHandler {
        val commandLine = GeneralCommandLine(buildDapCommand(geblangPath))
        return startServer(commandLine)
    }

    override fun getDapParameters(): Map<String, Any> = emptyMap()

    override fun getFileType(): FileType = GeblangFileType
}

/**
 * Pure helper (no IDE dependencies) building the argument list for launching
 * `geblang dap`, so it can be unit-tested directly without a running IDE process.
 *
 * @param geblangPath absolute path or bare name ("geblang") resolved on PATH
 */
fun buildDapCommand(geblangPath: String): List<String> = listOf(geblangPath, "dap")
