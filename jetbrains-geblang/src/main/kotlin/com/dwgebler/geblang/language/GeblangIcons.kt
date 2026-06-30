package com.dwgebler.geblang.language

import com.intellij.openapi.util.IconLoader

/**
 * Icon holder for Geblang plugin. Icons are loaded lazily from resources/icons/.
 */
object GeblangIcons {
    @JvmField
    val FILE = IconLoader.getIcon("/icons/geblang.svg", GeblangIcons::class.java)
}
