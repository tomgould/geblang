package com.dwgebler.geblang.highlighting

import com.intellij.lang.annotation.AnnotationHolder
import com.intellij.lang.annotation.Annotator
import com.intellij.lang.annotation.HighlightSeverity
import com.intellij.openapi.util.TextRange
import com.intellij.psi.PsiElement

/**
 * Additive annotator that highlights `${...}` interpolation spans inside
 * Geblang STRING leaves. The lexer emits the whole string as one opaque
 * STRING token; this annotator layers extra highlighting on top of the
 * sub-ranges that are interpolation expressions, without altering the
 * underlying token stream or PSI tree in any way.
 */
class GeblangInterpolationAnnotator : Annotator {
    override fun annotate(element: PsiElement, holder: AnnotationHolder) {
        if (element.node.elementType != GeblangTokenTypes.STRING) return
        val leafStart = element.textRange.startOffset
        for (r in GeblangInterpolation.ranges(element.text)) {
            val range = TextRange(leafStart + r.first, leafStart + r.last + 1)
            holder.newSilentAnnotation(HighlightSeverity.INFORMATION)
                .range(range)
                .textAttributes(GeblangSyntaxHighlighter.INTERPOLATION)
                .create()
        }
    }
}
