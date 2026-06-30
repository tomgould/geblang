package com.dwgebler.geblang.highlighting

import com.intellij.openapi.fileTypes.SyntaxHighlighter
import com.intellij.openapi.fileTypes.SyntaxHighlighterFactory
import com.intellij.openapi.project.Project
import com.intellij.openapi.vfs.VirtualFile

/**
 * Factory that produces the Geblang syntax highlighter.
 * Registered in plugin.xml under lang.syntaxHighlighterFactory.
 */
class GeblangSyntaxHighlighterFactory : SyntaxHighlighterFactory() {
    override fun getSyntaxHighlighter(project: Project?, virtualFile: VirtualFile?): SyntaxHighlighter =
        GeblangSyntaxHighlighter()
}
