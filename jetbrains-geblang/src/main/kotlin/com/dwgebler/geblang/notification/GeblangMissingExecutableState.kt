package com.dwgebler.geblang.notification

import com.intellij.openapi.components.Service
import com.intellij.openapi.project.Project

/**
 * Project-level, in-memory flag guarding against repeat "geblang executable not
 * found" notifications. One project instance lives for the lifetime of the
 * project window, so this naturally resets on a fresh IDE/project session.
 */
@Service(Service.Level.PROJECT)
class GeblangMissingExecutableState {

    @Volatile
    var hasNotified: Boolean = false

    companion object {
        fun getInstance(project: Project): GeblangMissingExecutableState =
            project.getService(GeblangMissingExecutableState::class.java)
    }
}
