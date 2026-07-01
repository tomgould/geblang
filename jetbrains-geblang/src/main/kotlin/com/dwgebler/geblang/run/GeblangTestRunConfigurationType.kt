package com.dwgebler.geblang.run

import com.dwgebler.geblang.language.GeblangIcons
import com.intellij.execution.configurations.ConfigurationFactory
import com.intellij.execution.configurations.ConfigurationTypeBase
import com.intellij.execution.configurations.RunConfiguration
import com.intellij.openapi.project.Project
import com.intellij.openapi.util.NotNullLazyValue

/**
 * Registers the "Geblang Test" run configuration type, shown in the
 * Run/Debug Configurations dialog and the gutter "create configuration" menu.
 */
class GeblangTestRunConfigurationType : ConfigurationTypeBase(
    ID,
    "Geblang Test",
    "Runs Geblang tests via `geblang test --format teamcity`",
    NotNullLazyValue.createConstantValue(GeblangIcons.FILE)
) {

    init {
        addFactory(GeblangTestConfigurationFactory(this))
    }

    companion object {
        const val ID: String = "GeblangTestRunConfigurationType"
    }
}

class GeblangTestConfigurationFactory(type: GeblangTestRunConfigurationType) : ConfigurationFactory(type) {

    override fun getId(): String = GeblangTestRunConfigurationType.ID

    override fun createTemplateConfiguration(project: Project): RunConfiguration =
        GeblangTestRunConfiguration(project, this, "Geblang Test")
}
