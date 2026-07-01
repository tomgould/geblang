package com.dwgebler.geblang.run

import com.intellij.execution.Executor
import com.intellij.execution.configurations.RunConfiguration
import com.intellij.execution.testframework.sm.runner.SMTRunnerConsoleProperties
import com.intellij.execution.testframework.sm.runner.SMTestLocator

/**
 * SM test runner console properties for the "Geblang" test framework. Wires
 * [GeblangTestLocator] so test tree entries support navigation back to source.
 */
class GeblangTestConsoleProperties(config: RunConfiguration, executor: Executor) :
    SMTRunnerConsoleProperties(config, FRAMEWORK_NAME, executor) {

    init {
        isIdBasedTestTree = false
    }

    override fun getTestLocator(): SMTestLocator = GeblangTestLocator

    companion object {
        const val FRAMEWORK_NAME: String = "Geblang"
    }
}
