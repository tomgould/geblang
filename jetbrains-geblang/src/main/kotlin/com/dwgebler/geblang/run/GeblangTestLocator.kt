package com.dwgebler.geblang.run

import com.intellij.execution.Location
import com.intellij.execution.PsiLocation
import com.intellij.execution.testframework.sm.runner.SMTestLocator
import com.intellij.openapi.project.Project
import com.intellij.openapi.vfs.VirtualFile
import com.intellij.psi.PsiElement
import com.intellij.psi.PsiFile
import com.intellij.psi.PsiManager
import com.intellij.psi.search.FilenameIndex
import com.intellij.psi.search.GlobalSearchScope

/**
 * A parsed `geblang_test://<Class>` or `geblang_test://<Class>/<method>` locator path.
 * [className] is never blank when this class is successfully constructed via
 * [GeblangTestLocator.parsePath].
 */
data class GeblangTestLocation(val className: String, val methodName: String?)

/**
 * Resolves `geblang_test://<Class>` and `geblang_test://<Class>/<method>` locations
 * produced by [GeblangTestConsoleProperties] back to a source location, so the test
 * tree supports double-click navigation.
 *
 * Geblang has no PSI parser yet (the plugin only registers a lexer-based syntax
 * highlighter), so there is no structured AST to resolve a class/method declaration
 * against. This locator does a lightweight, best-effort text scan of `.gb` files in
 * the given search scope for `class <Class>` (and, if a method is requested,
 * `func <method>` following it) and returns a location pointing at that file. If
 * nothing is found it returns an empty list rather than throwing, matching the
 * documented "best effort - do not crash" contract for [SMTestLocator].
 */
object GeblangTestLocator : SMTestLocator {

    const val PROTOCOL_ID: String = "geblang_test"

    /**
     * Parses the locator path portion of a `geblang_test://...` URL (i.e. everything
     * after the protocol) into a class name and optional method name. Returns `null`
     * for a malformed path (blank, or a blank class name before the separator).
     *
     * Pure function - no IDE dependencies - so it is unit-testable directly.
     */
    fun parsePath(path: String): GeblangTestLocation? {
        val separatorIndex = path.indexOf('/')
        val className = if (separatorIndex >= 0) path.substring(0, separatorIndex) else path
        val methodName = if (separatorIndex >= 0) path.substring(separatorIndex + 1) else null
        if (className.isBlank()) return null
        return GeblangTestLocation(className, methodName?.takeIf { it.isNotBlank() })
    }

    override fun getLocation(
        protocolId: String,
        path: String,
        project: Project,
        scope: GlobalSearchScope
    ): List<Location<out PsiElement>> {
        if (protocolId != PROTOCOL_ID) return emptyList()
        val parsed = parsePath(path) ?: return emptyList()
        val className = parsed.className
        val methodName = parsed.methodName

        val psiManager = PsiManager.getInstance(project)
        val candidateFiles = FilenameIndex.getAllFilesByExt(project, "gb", scope)

        for (virtualFile in candidateFiles) {
            val psiFile = psiManager.findFile(virtualFile) ?: continue
            val text = virtualFile.let { readTextSafely(it) } ?: continue
            if (!text.contains("class $className")) continue

            if (methodName != null) {
                val methodOffset = findMethodOffset(text, methodName)
                if (methodOffset >= 0) {
                    val location = elementAtOffset(psiFile, methodOffset, project)
                    if (location != null) return listOf(location)
                }
            }

            val classOffset = text.indexOf("class $className")
            val location = elementAtOffset(psiFile, classOffset, project) ?: continue
            return listOf(location)
        }

        return emptyList()
    }

    private fun readTextSafely(virtualFile: VirtualFile): String? {
        return try {
            String(virtualFile.contentsToByteArray(), virtualFile.charset)
        } catch (e: Exception) {
            null
        }
    }

    /**
     * Finds the offset of a `func <methodName>` declaration. Scans forward from the
     * start of the file - good enough for the common case of one test class per file,
     * which is how Geblang test files are conventionally structured.
     */
    private fun findMethodOffset(text: String, methodName: String): Int {
        return text.indexOf("func $methodName(")
    }

    private fun elementAtOffset(psiFile: PsiFile, offset: Int, project: Project): Location<out PsiElement>? {
        if (offset < 0) return null
        val element = psiFile.findElementAt(offset) ?: psiFile
        return PsiLocation.fromPsiElement(project, element)
    }
}
