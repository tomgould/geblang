package com.dwgebler.geblang.notification

import com.dwgebler.geblang.language.GeblangFileType
import com.dwgebler.geblang.settings.GeblangConfigurable
import com.dwgebler.geblang.settings.GeblangSettings
import com.intellij.notification.NotificationAction
import com.intellij.notification.NotificationGroupManager
import com.intellij.notification.NotificationType
import com.intellij.openapi.fileEditor.FileEditorManager
import com.intellij.openapi.fileEditor.FileEditorManagerListener
import com.intellij.openapi.options.ShowSettingsUtil
import com.intellij.openapi.project.Project
import com.intellij.openapi.vfs.VirtualFile

/**
 * Warns the user, at most once per project session, when the configured
 * `geblang` executable cannot be resolved. Without the binary, LSP4IJ has
 * nothing to launch and all LSP-backed features (diagnostics, completion,
 * hover, go-to-definition, formatting, etc.) stay disabled.
 *
 * Registered as a [FileEditorManagerListener] via `<projectListeners>` in
 * plugin.xml, and triggers the check the first time a `.gb` file is opened.
 */
class GeblangMissingExecutableNotifier : FileEditorManagerListener {

    override fun fileOpened(source: FileEditorManager, file: VirtualFile) {
        if (file.fileType != GeblangFileType) return

        val project = source.project
        val state = GeblangMissingExecutableState.getInstance(project)
        if (state.hasNotified) return

        val configuredPath = GeblangSettings.getInstance().geblangExecutablePath
        if (GeblangExecutable.resolve(configuredPath) != null) return

        state.hasNotified = true
        notifyMissingExecutable(project)
    }

    private fun notifyMissingExecutable(project: Project) {
        val group = NotificationGroupManager.getInstance().getNotificationGroup(NOTIFICATION_GROUP_ID)
        val notification = group.createNotification(
            "Geblang",
            "The geblang executable could not be found. LSP features " +
                "(diagnostics, completion, hover, go-to-definition, formatting) are disabled " +
                "until it is configured.",
            NotificationType.WARNING
        )
        notification.addAction(
            NotificationAction.createSimpleExpiring("Configure...") {
                ShowSettingsUtil.getInstance().showSettingsDialog(project, GeblangConfigurable::class.java)
            }
        )
        notification.notify(project)
    }

    companion object {
        const val NOTIFICATION_GROUP_ID = "Geblang"
    }
}
