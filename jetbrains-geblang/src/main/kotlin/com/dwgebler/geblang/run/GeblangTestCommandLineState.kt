package com.dwgebler.geblang.run

import com.dwgebler.geblang.notification.GeblangExecutable
import com.dwgebler.geblang.settings.GeblangSettings
import com.intellij.execution.Executor
import com.intellij.execution.configurations.CommandLineState
import com.intellij.execution.configurations.GeneralCommandLine
import com.intellij.execution.process.KillableColoredProcessHandler
import com.intellij.execution.process.ProcessHandler
import com.intellij.execution.process.ProcessTerminatedListener
import com.intellij.execution.runners.ExecutionEnvironment
import com.intellij.execution.testframework.sm.SMTestRunnerConnectionUtil
import com.intellij.execution.ui.ConsoleView
import java.io.File

/**
 * Builds and runs `<geblang> test --format teamcity <target> [--tag <tag>]`, attaching
 * an [com.intellij.execution.testframework.sm.runner.ui.SMTRunnerConsoleView] so the
 * TeamCity service messages on stdout render as IntelliJ's native test tree.
 */
class GeblangTestCommandLineState(
    environment: ExecutionEnvironment,
    private val configuration: GeblangTestRunConfiguration
) : CommandLineState(environment) {

    override fun startProcess(): ProcessHandler {
        val settings = GeblangSettings.getInstance()
        val executableFile = GeblangExecutable.resolve(settings.geblangExecutablePath)
        val executablePath = executableFile?.path ?: "geblang"

        val commandLine = GeneralCommandLine(executablePath)
        commandLine.addParameters(buildTestArguments(configuration.target, configuration.tag))

        val workDir = configuration.workingDirectory.ifBlank { environment.project.basePath }
        if (!workDir.isNullOrBlank()) {
            commandLine.setWorkDirectory(File(workDir))
        }

        val handler = KillableColoredProcessHandler(commandLine)
        ProcessTerminatedListener.attach(handler)
        return handler
    }

    override fun createConsole(executor: Executor): ConsoleView {
        val consoleProperties = GeblangTestConsoleProperties(configuration, executor)
        return SMTestRunnerConnectionUtil.createConsole(consoleProperties)
    }
}

/**
 * Pure helper (no IDE dependencies) building the argument list for
 * `geblang test --format teamcity [--tag <tag>] <target>`, so it can be unit-tested
 * directly without a running IDE process.
 */
fun buildTestArguments(target: String, tag: String): List<String> {
    val args = mutableListOf("test", "--format", "teamcity")
    if (tag.isNotBlank()) {
        args.add("--tag")
        args.add(tag)
    }
    args.add(target)
    return args
}
