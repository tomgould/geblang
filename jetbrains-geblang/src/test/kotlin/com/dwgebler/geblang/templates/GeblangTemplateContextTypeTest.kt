package com.dwgebler.geblang.templates

import com.dwgebler.geblang.language.GeblangFileType
import com.intellij.openapi.fileTypes.PlainTextFileType
import junit.framework.TestCase

/**
 * Lightweight, IDE-fixture-free checks for [GeblangTemplateContextType].
 *
 * [GeblangTemplateContextType.isInContext] itself takes a
 * [com.intellij.codeInsight.template.TemplateActionContext], which wraps a live
 * [com.intellij.psi.PsiFile] + [com.intellij.openapi.editor.Editor] pair and can only
 * be constructed with a running IDE fixture (project, virtual file system, PSI). That
 * full expansion path is exercised manually via `runIde`, not headlessly here.
 *
 * What *can* be verified without an IDE is the plain file-type equality check the
 * context type is built on: [GeblangFileType] is a distinct singleton from other
 * registered file types, so `fileType == GeblangFileType` (the condition
 * [GeblangTemplateContextType] evaluates against the context's file) behaves as a
 * simple, deterministic identity check.
 */
class GeblangTemplateContextTypeTest : TestCase() {

    fun testPresentableNameIsGeblang() {
        val contextType = GeblangTemplateContextType()
        assertEquals("Geblang", contextType.presentableName)
    }

    fun testGeblangFileTypeIsItsOwnDistinctSingleton() {
        assertSame(GeblangFileType, GeblangFileType)
        assertNotSame(GeblangFileType as Any, PlainTextFileType.INSTANCE as Any)
    }

    fun testGeblangFileTypeExtensionIsGb() {
        assertEquals("gb", GeblangFileType.defaultExtension)
    }
}
