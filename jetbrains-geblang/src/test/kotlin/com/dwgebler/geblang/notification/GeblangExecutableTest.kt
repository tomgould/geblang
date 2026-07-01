package com.dwgebler.geblang.notification

import junit.framework.TestCase
import java.io.File

/**
 * Unit tests for the pure [GeblangExecutable.resolve] helper.
 *
 * These deliberately avoid any IDE/UI/notification machinery — [GeblangExecutable]
 * has no dependency on the IntelliJ Platform test fixtures beyond
 * [com.intellij.execution.configurations.PathEnvironmentVariableUtil], which works
 * fine as a plain JVM call, so a plain JUnit [TestCase] is sufficient here.
 */
class GeblangExecutableTest : TestCase() {

    fun testAbsoluteNonExistentPathResolvesToNull() {
        val result = GeblangExecutable.resolve("/definitely/does/not/exist/geblang-binary-xyz")
        assertNull(result)
    }

    fun testKnownExecutableAbsolutePathResolves() {
        val result = GeblangExecutable.resolve("/bin/sh")
        assertNotNull(result)
        assertEquals(File("/bin/sh").canonicalFile, result!!.canonicalFile)
    }

    fun testBareNameNotOnPathResolvesToNull() {
        val result = GeblangExecutable.resolve("geblang-binary-that-does-not-exist-xyz-123")
        assertNull(result)
    }

    fun testBlankPathIsTreatedAsBareGeblangName() {
        // "geblang" is very unlikely to be installed in this test environment;
        // this asserts the blank-path branch behaves identically to passing
        // "geblang" explicitly (both go through the PATH lookup, neither throws).
        val blankResult = GeblangExecutable.resolve("")
        val explicitResult = GeblangExecutable.resolve("geblang")
        assertEquals(explicitResult?.canonicalPath, blankResult?.canonicalPath)
    }

    fun testAbsolutePathToNonExecutableFileResolvesToNull() {
        // /etc/hosts exists on any Linux CI box but is not executable.
        val hosts = File("/etc/hosts")
        if (!hosts.exists()) return // environment without /etc/hosts — nothing to assert
        val result = GeblangExecutable.resolve(hosts.absolutePath)
        assertNull(result)
    }
}
