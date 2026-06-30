package com.dwgebler.geblang.language

import com.intellij.lang.Language

/**
 * Singleton Language instance for Geblang.
 * Used by IntelliJ to associate file types, highlighters, and other editor services.
 */
object GeblangLanguage : Language("Geblang") {
    private fun readResolve(): Any = GeblangLanguage
}
