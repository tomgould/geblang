package com.dwgebler.geblang.actions

import com.dwgebler.geblang.language.GeblangFileType
import com.dwgebler.geblang.language.GeblangIcons
import com.intellij.ide.actions.CreateFileFromTemplateAction
import com.intellij.ide.actions.CreateFileFromTemplateDialog
import com.intellij.openapi.project.Project
import com.intellij.psi.PsiDirectory

/**
 * "New > Geblang File" action: offers the bundled Geblang file templates
 * (plain file, class, module, test) in the standard create-from-template
 * dialog and scaffolds a new `.gb` file from the chosen one.
 *
 * The four templates are internal `FileTemplate`s shipped under
 * `resources/fileTemplates/internal/` and are also exposed for editing under
 * Settings > Editor > File and Code Templates via [GeblangFileTemplateGroupFactory].
 */
class GeblangCreateFileAction :
    CreateFileFromTemplateAction(ACTION_NAME, "Creates a new Geblang file", GeblangIcons.FILE) {

    override fun buildDialog(project: Project, directory: PsiDirectory, builder: CreateFileFromTemplateDialog.Builder) {
        builder
            .setTitle(ACTION_NAME)
            .addKind("File", GeblangIcons.FILE, "Geblang File")
            .addKind("Class", GeblangIcons.FILE, "Geblang Class")
            .addKind("Module", GeblangIcons.FILE, "Geblang Module")
            .addKind("Test", GeblangIcons.FILE, "Geblang Test")
    }

    override fun getActionName(directory: PsiDirectory, newName: String, templateName: String): String = ACTION_NAME

    companion object {
        private const val ACTION_NAME = "Geblang File"
    }
}
