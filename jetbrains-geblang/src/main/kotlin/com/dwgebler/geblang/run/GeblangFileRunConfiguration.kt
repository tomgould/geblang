package com.dwgebler.geblang.run

import com.intellij.execution.Executor
import com.intellij.execution.configurations.ConfigurationFactory
import com.intellij.execution.configurations.LocatableConfigurationBase
import com.intellij.execution.configurations.RunConfiguration
import com.intellij.execution.configurations.RuntimeConfigurationException
import com.intellij.execution.runners.ExecutionEnvironment
import com.intellij.openapi.options.SettingsEditor
import com.intellij.openapi.project.Project
import org.jdom.Element

/**
 * Run configuration for `geblang run <file> [args]`.
 *
 * Settings persisted across IDE restarts (see [readExternal]/[writeExternal]):
 * - [target]: the `.gb` file to run (required to actually run).
 * - [workingDirectory]: optional working directory for the process; defaults to the
 *   project base path when blank.
 * - [programArguments]: optional space-separated arguments forwarded after the file.
 */
class GeblangFileRunConfiguration(project: Project, factory: ConfigurationFactory, name: String) :
    LocatableConfigurationBase<RunProfileStateUnused>(project, factory, name) {

    var target: String = ""
    var workingDirectory: String = ""
    var programArguments: String = ""

    override fun getConfigurationEditor(): SettingsEditor<out RunConfiguration> =
        GeblangFileSettingsEditor(project)

    override fun checkConfiguration() {
        if (target.isBlank()) {
            throw RuntimeConfigurationException("Specify a Geblang file to run")
        }
    }

    override fun getState(executor: Executor, environment: ExecutionEnvironment): GeblangFileCommandLineState {
        return GeblangFileCommandLineState(environment, this)
    }

    override fun readExternal(element: Element) {
        super.readExternal(element)
        target = element.getAttributeValue(ATTR_TARGET) ?: ""
        workingDirectory = element.getAttributeValue(ATTR_WORKING_DIR) ?: ""
        programArguments = element.getAttributeValue(ATTR_PROGRAM_ARGS) ?: ""
    }

    override fun writeExternal(element: Element) {
        super.writeExternal(element)
        element.setAttribute(ATTR_TARGET, target)
        element.setAttribute(ATTR_WORKING_DIR, workingDirectory)
        element.setAttribute(ATTR_PROGRAM_ARGS, programArguments)
    }

    companion object {
        private const val ATTR_TARGET = "geblangTarget"
        private const val ATTR_WORKING_DIR = "geblangWorkingDirectory"
        private const val ATTR_PROGRAM_ARGS = "geblangProgramArguments"
    }
}
