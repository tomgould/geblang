package com.dwgebler.geblang.templates

import com.dwgebler.geblang.language.GeblangFileType
import com.intellij.codeInsight.template.TemplateActionContext
import com.intellij.codeInsight.template.TemplateContextType

/**
 * Live template context for Geblang (.gb) files.
 *
 * Registered with the stable contextId "GEBLANG" via the `liveTemplateContext`
 * extension point in plugin.xml; every template in `liveTemplates/Geblang.xml`
 * carries a matching `<option name="GEBLANG" value="true"/>` context entry so
 * it is only offered while editing a Geblang file.
 */
class GeblangTemplateContextType : TemplateContextType("Geblang") {
    override fun isInContext(context: TemplateActionContext): Boolean =
        context.file.fileType == GeblangFileType
}
