package com.dwgebler.geblang.lsp

import com.redhat.devtools.lsp4ij.server.ProcessStreamConnectionProvider

/**
 * Launches `geblang lsp` as a child process and communicates over stdio.
 *
 * ProcessStreamConnectionProvider handles:
 *  - Starting the process with the given command list
 *  - Connecting LSP4IJ's input/output streams to the process's stdout/stdin
 *  - Restarting on crash (handled by LSP4IJ lifecycle management)
 *
 * @param geblangPath absolute path or bare name ("geblang") resolved on PATH
 */
class GeblangStreamConnectionProvider(geblangPath: String) : ProcessStreamConnectionProvider() {
    init {
        commands = listOf(geblangPath, "lsp")
    }
}
