package com.dwgebler.geblang.run

import com.dwgebler.geblang.notification.GeblangExecutable
import com.dwgebler.geblang.settings.GeblangSettings
import com.intellij.execution.configurations.CommandLineState
import com.intellij.execution.configurations.GeneralCommandLine
import com.intellij.execution.process.KillableColoredProcessHandler
import com.intellij.execution.process.ProcessHandler
import com.intellij.execution.process.ProcessTerminatedListener
import com.intellij.execution.runners.ExecutionEnvironment
import java.io.File

/**
 * Builds and runs `<geblang> run <file> [args]`, attaching a plain console (the
 * default [com.intellij.execution.ui.ConsoleView] built by
 * [com.intellij.execution.configurations.CommandLineState.createConsole] from this
 * state's console builder) - unlike [GeblangTestCommandLineState], this does NOT
 * attach the SMTestRunner test tree, since running a file is not running tests.
 */
class GeblangFileCommandLineState(
    environment: ExecutionEnvironment,
    private val configuration: GeblangFileRunConfiguration
) : CommandLineState(environment) {

    override fun startProcess(): ProcessHandler {
        val settings = GeblangSettings.getInstance()
        val executableFile = GeblangExecutable.resolve(settings.geblangExecutablePath)
        val executablePath = executableFile?.path ?: "geblang"

        val commandLine = GeneralCommandLine(executablePath)
        commandLine.addParameters(buildRunArguments(configuration.target, configuration.programArguments))

        val workDir = configuration.workingDirectory.ifBlank { environment.project.basePath }
        if (!workDir.isNullOrBlank()) {
            commandLine.setWorkDirectory(File(workDir))
        }

        val handler = KillableColoredProcessHandler(commandLine)
        ProcessTerminatedListener.attach(handler)
        return handler
    }
}

/**
 * Pure helper (no IDE dependencies) building the argument list for
 * `geblang run <file>` plus any extra arguments, so it can be unit-tested directly
 * without a running IDE process. [programArguments] is whitespace-split; blank
 * entries (including an entirely blank string) are dropped.
 */
fun buildRunArguments(target: String, programArguments: String): List<String> {
    val args = mutableListOf("run", target)
    args.addAll(programArguments.split(Regex("\\s+")).filter { it.isNotBlank() })
    return args
}
