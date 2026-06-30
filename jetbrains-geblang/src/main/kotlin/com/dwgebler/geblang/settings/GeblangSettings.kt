package com.dwgebler.geblang.settings

import com.intellij.openapi.application.ApplicationManager
import com.intellij.openapi.components.PersistentStateComponent
import com.intellij.openapi.components.State
import com.intellij.openapi.components.Storage

/**
 * Application-level persistent settings for the Geblang plugin.
 * Stored in geblang.xml inside the IDE config directory.
 */
@State(
    name = "GeblangSettings",
    storages = [Storage("geblang.xml")]
)
class GeblangSettings : PersistentStateComponent<GeblangSettings.State> {

    data class State(
        var geblangExecutablePath: String = "geblang"
    )

    private var myState = State()

    override fun getState(): State = myState

    override fun loadState(state: State) {
        myState = state
    }

    var geblangExecutablePath: String
        get() = myState.geblangExecutablePath
        set(value) { myState.geblangExecutablePath = value }

    companion object {
        fun getInstance(): GeblangSettings =
            ApplicationManager.getApplication().getService(GeblangSettings::class.java)
    }
}
