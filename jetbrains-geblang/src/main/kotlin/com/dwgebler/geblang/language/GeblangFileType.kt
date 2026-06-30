package com.dwgebler.geblang.language

import com.intellij.openapi.fileTypes.LanguageFileType
import javax.swing.Icon

/**
 * Registers .gb as the Geblang file extension.
 */
object GeblangFileType : LanguageFileType(GeblangLanguage) {
    private fun readResolve(): Any = GeblangFileType

    override fun getName(): String = "Geblang"
    override fun getDescription(): String = "Geblang language source file"
    override fun getDefaultExtension(): String = "gb"
    override fun getIcon(): Icon = GeblangIcons.FILE
}
