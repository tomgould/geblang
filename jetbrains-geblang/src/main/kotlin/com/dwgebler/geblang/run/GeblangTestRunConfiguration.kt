package com.dwgebler.geblang.run

import com.intellij.execution.Executor
import com.intellij.execution.configurations.ConfigurationFactory
import com.intellij.execution.configurations.LocatableConfigurationBase
import com.intellij.execution.configurations.RunConfiguration
import com.intellij.execution.configurations.RunConfigurationBase
import com.intellij.execution.configurations.RuntimeConfigurationException
import com.intellij.execution.runners.ExecutionEnvironment
import com.intellij.openapi.options.SettingsEditor
import com.intellij.openapi.project.Project
import org.jdom.Element

/**
 * Run configuration for `geblang test --format teamcity <target>`.
 *
 * Settings persisted across IDE restarts (see [readExternal]/[writeExternal]):
 * - [target]: a `.gb` file or a directory to test (required to actually run).
 * - [workingDirectory]: optional working directory for the process; defaults to the
 *   project base path when blank.
 * - [tag]: optional `--tag` filter, forwarded as `--tag <tag>` when non-blank.
 */
class GeblangTestRunConfiguration(project: Project, factory: ConfigurationFactory, name: String) :
    LocatableConfigurationBase<RunProfileStateUnused>(project, factory, name) {

    var target: String = ""
    var workingDirectory: String = ""
    var tag: String = ""

    override fun getConfigurationEditor(): SettingsEditor<out RunConfiguration> =
        GeblangTestSettingsEditor(project)

    override fun checkConfiguration() {
        if (target.isBlank()) {
            throw RuntimeConfigurationException("Specify a Geblang test file or directory to run")
        }
    }

    override fun getState(executor: Executor, environment: ExecutionEnvironment): GeblangTestCommandLineState {
        return GeblangTestCommandLineState(environment, this)
    }

    override fun readExternal(element: Element) {
        super.readExternal(element)
        target = element.getAttributeValue(ATTR_TARGET) ?: ""
        workingDirectory = element.getAttributeValue(ATTR_WORKING_DIR) ?: ""
        tag = element.getAttributeValue(ATTR_TAG) ?: ""
    }

    override fun writeExternal(element: Element) {
        super.writeExternal(element)
        element.setAttribute(ATTR_TARGET, target)
        element.setAttribute(ATTR_WORKING_DIR, workingDirectory)
        element.setAttribute(ATTR_TAG, tag)
    }

    companion object {
        private const val ATTR_TARGET = "geblangTarget"
        private const val ATTR_WORKING_DIR = "geblangWorkingDirectory"
        private const val ATTR_TAG = "geblangTag"
    }
}

/**
 * [RunConfigurationBase] is generic over a state type used by the deprecated
 * [RunConfigurationBase.getState]/[RunConfigurationBase.loadState]
 * ([com.intellij.openapi.components.PersistentStateComponent]-style) persistence
 * path. [GeblangTestRunConfiguration] does not use that path - it persists settings
 * itself via [GeblangTestRunConfiguration.readExternal]/[GeblangTestRunConfiguration.writeExternal] -
 * so the type parameter is an unused marker type.
 */
class RunProfileStateUnused private constructor()
