package com.dwgebler.geblang.run

import com.intellij.openapi.fileChooser.FileChooserDescriptorFactory
import com.intellij.openapi.options.SettingsEditor
import com.intellij.openapi.ui.TextBrowseFolderListener
import com.intellij.openapi.ui.TextFieldWithBrowseButton
import java.awt.GridBagConstraints
import java.awt.GridBagLayout
import java.awt.Insets
import javax.swing.JComponent
import javax.swing.JLabel
import javax.swing.JPanel
import javax.swing.JTextField

/**
 * Settings UI for a [GeblangTestRunConfiguration]: a target file-or-directory picker,
 * an optional working directory picker, and an optional `--tag` filter field.
 */
class GeblangTestSettingsEditor(project: com.intellij.openapi.project.Project) :
    SettingsEditor<GeblangTestRunConfiguration>() {

    private val targetField = TextFieldWithBrowseButton().apply {
        addBrowseFolderListener(
            TextBrowseFolderListener(
                FileChooserDescriptorFactory.createSingleFileOrFolderDescriptor(),
                project
            )
        )
    }

    private val workingDirectoryField = TextFieldWithBrowseButton().apply {
        addBrowseFolderListener(
            TextBrowseFolderListener(
                FileChooserDescriptorFactory.createSingleFolderDescriptor(),
                project
            )
        )
    }

    private val tagField = JTextField(20)

    override fun resetEditorFrom(configuration: GeblangTestRunConfiguration) {
        targetField.text = configuration.target
        workingDirectoryField.text = configuration.workingDirectory
        tagField.text = configuration.tag
    }

    override fun applyEditorTo(configuration: GeblangTestRunConfiguration) {
        configuration.target = targetField.text.trim()
        configuration.workingDirectory = workingDirectoryField.text.trim()
        configuration.tag = tagField.text.trim()
    }

    override fun createEditor(): JComponent {
        val panel = JPanel(GridBagLayout())
        val gbc = GridBagConstraints().apply {
            insets = Insets(4, 4, 4, 4)
            anchor = GridBagConstraints.WEST
            fill = GridBagConstraints.HORIZONTAL
        }

        gbc.gridx = 0; gbc.gridy = 0; gbc.weightx = 0.0
        panel.add(JLabel("Target (.gb file or directory):"), gbc)
        gbc.gridx = 1; gbc.weightx = 1.0
        panel.add(targetField, gbc)

        gbc.gridx = 0; gbc.gridy = 1; gbc.weightx = 0.0
        panel.add(JLabel("Working directory (optional):"), gbc)
        gbc.gridx = 1; gbc.weightx = 1.0
        panel.add(workingDirectoryField, gbc)

        gbc.gridx = 0; gbc.gridy = 2; gbc.weightx = 0.0
        panel.add(JLabel("Tag filter (optional):"), gbc)
        gbc.gridx = 1; gbc.weightx = 1.0
        panel.add(tagField, gbc)

        return panel
    }
}
