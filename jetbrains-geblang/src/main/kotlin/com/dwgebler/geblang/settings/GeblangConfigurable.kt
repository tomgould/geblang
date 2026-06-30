package com.dwgebler.geblang.settings

import com.intellij.openapi.options.Configurable
import javax.swing.*
import java.awt.BorderLayout
import java.awt.GridBagConstraints
import java.awt.GridBagLayout
import java.awt.Insets

/**
 * Settings UI panel under Settings > Languages & Frameworks > Geblang.
 * Allows the user to configure the path to the geblang executable.
 */
class GeblangConfigurable : Configurable {

    private var pathField: JTextField? = null
    private var panel: JPanel? = null

    override fun getDisplayName(): String = "Geblang"

    override fun createComponent(): JComponent {
        val field = JTextField(40)
        pathField = field

        val outerPanel = JPanel(GridBagLayout())
        val gbc = GridBagConstraints().apply {
            insets = Insets(4, 4, 4, 4)
            anchor = GridBagConstraints.WEST
        }

        gbc.gridx = 0; gbc.gridy = 0
        outerPanel.add(JLabel("Geblang executable path:"), gbc)

        gbc.gridx = 1; gbc.fill = GridBagConstraints.HORIZONTAL; gbc.weightx = 1.0
        outerPanel.add(field, gbc)

        val hint = JLabel("<html><small>Path to the <code>geblang</code> binary. " +
                "Set to just <code>geblang</code> if it is on your PATH. " +
                "LSP features (diagnostics, completion, hover) require this binary.</small></html>")
        gbc.gridx = 0; gbc.gridy = 1; gbc.gridwidth = 2; gbc.fill = GridBagConstraints.HORIZONTAL
        outerPanel.add(hint, gbc)

        val wrapper = JPanel(BorderLayout())
        wrapper.add(outerPanel, BorderLayout.NORTH)
        panel = wrapper
        return wrapper
    }

    override fun isModified(): Boolean {
        val settings = GeblangSettings.getInstance()
        return pathField?.text != settings.geblangExecutablePath
    }

    override fun apply() {
        val settings = GeblangSettings.getInstance()
        settings.geblangExecutablePath = pathField?.text?.trim() ?: "geblang"
    }

    override fun reset() {
        val settings = GeblangSettings.getInstance()
        pathField?.text = settings.geblangExecutablePath
    }

    override fun disposeUIResources() {
        pathField = null
        panel = null
    }
}
