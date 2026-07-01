package com.dwgebler.geblang.notification

import com.intellij.execution.configurations.PathEnvironmentVariableUtil
import java.io.File

/**
 * Pure helper for resolving the configured `geblang` executable path to a real,
 * executable [File]. Contains no IDE/UI side effects so it can be unit-tested
 * directly.
 */
object GeblangExecutable {

    /**
     * Resolves [path] to an executable file, or `null` if it cannot be found.
     *
     * - A blank path is treated as the bare name `"geblang"`.
     * - A path containing a file separator (`/` or, on Windows, `\`) is treated as
     *   an absolute/relative path: it resolves only if that exact file exists and
     *   is executable.
     * - A bare name (no separator) is looked up on the `PATH` environment variable
     *   via [PathEnvironmentVariableUtil.findInPath].
     */
    fun resolve(path: String): File? {
        val effective = path.ifBlank { "geblang" }
        return if (effective.contains('/') || effective.contains(File.separatorChar)) {
            File(effective).takeIf { it.canExecute() }
        } else {
            PathEnvironmentVariableUtil.findInPath(effective)
        }
    }
}
