package com.dwgebler.geblang.highlighting

import com.dwgebler.geblang.language.GeblangIcons
import com.intellij.openapi.editor.colors.TextAttributesKey
import com.intellij.openapi.fileTypes.SyntaxHighlighter
import com.intellij.openapi.options.colors.AttributesDescriptor
import com.intellij.openapi.options.colors.ColorDescriptor
import com.intellij.openapi.options.colors.ColorSettingsPage
import javax.swing.Icon

/**
 * Registers Geblang colors in Settings > Editor > Color Scheme > Geblang.
 * Users can customise any token color from this page.
 */
class GeblangColorSettingsPage : ColorSettingsPage {

    private val descriptors = arrayOf(
        AttributesDescriptor("Comment",          GeblangSyntaxHighlighter.COMMENT),
        AttributesDescriptor("String",           GeblangSyntaxHighlighter.STRING),
        AttributesDescriptor("Number",           GeblangSyntaxHighlighter.NUMBER),
        AttributesDescriptor("Keyword",          GeblangSyntaxHighlighter.KEYWORD),
        AttributesDescriptor("Built-in type",    GeblangSyntaxHighlighter.TYPE),
        AttributesDescriptor("Constant (true/false/null/this)", GeblangSyntaxHighlighter.CONSTANT),
        AttributesDescriptor("Word operator (is/not/xor)",      GeblangSyntaxHighlighter.WORD_OP),
        AttributesDescriptor("Operator",         GeblangSyntaxHighlighter.OPERATOR),
        AttributesDescriptor("Identifier",       GeblangSyntaxHighlighter.IDENTIFIER),
        AttributesDescriptor("Bad character",    GeblangSyntaxHighlighter.BAD_CHAR),
    )

    override fun getIcon(): Icon = GeblangIcons.FILE
    override fun getHighlighter(): SyntaxHighlighter = GeblangSyntaxHighlighter()
    override fun getDemoText(): String = DEMO_TEXT
    override fun getAdditionalHighlightingTagToDescriptorMap(): Map<String, TextAttributesKey>? = null
    override fun getAttributeDescriptors(): Array<AttributesDescriptor> = descriptors
    override fun getColorDescriptors(): Array<ColorDescriptor> = ColorDescriptor.EMPTY_ARRAY
    override fun getDisplayName(): String = "Geblang"

    companion object {
        private val DEMO_TEXT = """
# This is a line comment
## This is a doc comment
/* Block comment */
/** Doc block comment */

module example

import std.io

# Note: // is integer division, NOT a comment
func divide(a int, b int) int {
    return a // b
}

class Animal {
    let name string
    let age int

    init(name string, age int) {
        this.name = name
        this.age = age
    }

    func speak() string {
        return "Hello from ${'$'}{this.name}"
    }
}

let greeting "Hello, World!"
let raw = 'no ${'$'}{interpolation} here'
let num = 42
let hex = 0xFF
let flt = 3.14f
let dec = 1_000_000

if num > 0 && num is int {
    for i in 0..10 {
        match i {
            case 0 -> "zero"
            default -> "other"
        }
    }
}

async func fetchData() Task {
    try {
        let result = await getData()
        return result
    } catch e {
        throw e
    }
}
""".trimIndent()
    }
}
