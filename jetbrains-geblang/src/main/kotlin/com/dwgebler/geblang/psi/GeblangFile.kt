package com.dwgebler.geblang.psi

import com.dwgebler.geblang.language.GeblangFileType
import com.dwgebler.geblang.language.GeblangLanguage
import com.intellij.extapi.psi.PsiFileBase
import com.intellij.openapi.fileTypes.FileType
import com.intellij.psi.FileViewProvider

/**
 * PSI file root for Geblang source files.
 *
 * This is a minimal PSI file: its children are a flat list of leaf nodes, one
 * per lexer token (see [GeblangParserDefinition]). There is no grammar - this
 * exists only so PSI-based platform features (folding, structure view,
 * run-line markers, spellchecking, TODO highlighting) have a tree to attach
 * to. Semantic analysis remains owned by the Geblang LSP server.
 */
class GeblangFile(viewProvider: FileViewProvider) : PsiFileBase(viewProvider, GeblangLanguage) {

    override fun getFileType(): FileType = GeblangFileType

    override fun toString(): String = "Geblang File"
}
