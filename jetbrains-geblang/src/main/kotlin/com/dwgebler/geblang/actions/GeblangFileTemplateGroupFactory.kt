package com.dwgebler.geblang.actions

import com.dwgebler.geblang.language.GeblangIcons
import com.intellij.ide.fileTemplates.FileTemplateGroupDescriptor
import com.intellij.ide.fileTemplates.FileTemplateGroupDescriptorFactory

/**
 * Exposes the bundled Geblang file templates under
 * Settings > Editor > File and Code Templates, grouped under "Geblang" with
 * the plugin's file icon, so users can review or customise them without
 * going through the New File dialog.
 */
class GeblangFileTemplateGroupFactory : FileTemplateGroupDescriptorFactory {

    override fun getFileTemplatesDescriptor(): FileTemplateGroupDescriptor {
        val group = FileTemplateGroupDescriptor("Geblang", GeblangIcons.FILE)
        group.addTemplate("Geblang File.gb")
        group.addTemplate("Geblang Class.gb")
        group.addTemplate("Geblang Module.gb")
        group.addTemplate("Geblang Test.gb")
        return group
    }
}
