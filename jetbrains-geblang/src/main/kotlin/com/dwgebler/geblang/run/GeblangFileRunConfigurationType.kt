package com.dwgebler.geblang.run

import com.dwgebler.geblang.language.GeblangIcons
import com.intellij.execution.configurations.ConfigurationFactory
import com.intellij.execution.configurations.ConfigurationTypeBase
import com.intellij.execution.configurations.RunConfiguration
import com.intellij.openapi.project.Project
import com.intellij.openapi.util.NotNullLazyValue

/**
 * Registers the "Geblang File" run configuration type, shown in the
 * Run/Debug Configurations dialog and the gutter "create configuration" menu.
 */
class GeblangFileRunConfigurationType : ConfigurationTypeBase(
    ID,
    "Geblang File",
    "Runs a Geblang file via `geblang run`",
    NotNullLazyValue.createConstantValue(GeblangIcons.FILE)
) {

    init {
        addFactory(GeblangFileConfigurationFactory(this))
    }

    companion object {
        const val ID: String = "GeblangFileRunConfigurationType"
    }
}

class GeblangFileConfigurationFactory(type: GeblangFileRunConfigurationType) : ConfigurationFactory(type) {

    override fun getId(): String = GeblangFileRunConfigurationType.ID

    override fun createTemplateConfiguration(project: Project): RunConfiguration =
        GeblangFileRunConfiguration(project, this, "Geblang File")
}
