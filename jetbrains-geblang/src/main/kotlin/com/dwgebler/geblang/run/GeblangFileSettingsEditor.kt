package com.dwgebler.geblang.run

import com.dwgebler.geblang.language.GeblangFileType
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
 * Settings UI for a [GeblangFileRunConfiguration]: a target `.gb` file picker, an
 * optional working directory picker, and optional program arguments field.
 */
class GeblangFileSettingsEditor(project: com.intellij.openapi.project.Project) :
    SettingsEditor<GeblangFileRunConfiguration>() {

    private val targetField = TextFieldWithBrowseButton().apply {
        addBrowseFolderListener(
            TextBrowseFolderListener(
                FileChooserDescriptorFactory.createSingleFileDescriptor(GeblangFileType),
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

    private val programArgumentsField = JTextField(20)

    override fun resetEditorFrom(configuration: GeblangFileRunConfiguration) {
        targetField.text = configuration.target
        workingDirectoryField.text = configuration.workingDirectory
        programArgumentsField.text = configuration.programArguments
    }

    override fun applyEditorTo(configuration: GeblangFileRunConfiguration) {
        configuration.target = targetField.text.trim()
        configuration.workingDirectory = workingDirectoryField.text.trim()
        configuration.programArguments = programArgumentsField.text.trim()
    }

    override fun createEditor(): JComponent {
        val panel = JPanel(GridBagLayout())
        val gbc = GridBagConstraints().apply {
            insets = Insets(4, 4, 4, 4)
            anchor = GridBagConstraints.WEST
            fill = GridBagConstraints.HORIZONTAL
        }

        gbc.gridx = 0; gbc.gridy = 0; gbc.weightx = 0.0
        panel.add(JLabel("Geblang file:"), gbc)
        gbc.gridx = 1; gbc.weightx = 1.0
        panel.add(targetField, gbc)

        gbc.gridx = 0; gbc.gridy = 1; gbc.weightx = 0.0
        panel.add(JLabel("Working directory (optional):"), gbc)
        gbc.gridx = 1; gbc.weightx = 1.0
        panel.add(workingDirectoryField, gbc)

        gbc.gridx = 0; gbc.gridy = 2; gbc.weightx = 0.0
        panel.add(JLabel("Program arguments (optional):"), gbc)
        gbc.gridx = 1; gbc.weightx = 1.0
        panel.add(programArgumentsField, gbc)

        return panel
    }
}
