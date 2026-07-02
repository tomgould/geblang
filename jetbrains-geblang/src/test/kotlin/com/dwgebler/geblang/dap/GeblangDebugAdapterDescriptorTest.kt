package com.dwgebler.geblang.dap

import junit.framework.TestCase

/**
 * Unit tests for [buildDapCommand], the pure argument-construction helper used by
 * [GeblangDebugAdapterDescriptor.startServer]. No IDE fixtures needed - plain values
 * in, plain list out - mirrors [com.dwgebler.geblang.run.GeblangFileCommandLineStateTest]
 * for `buildRunArguments`.
 */
class GeblangDebugAdapterDescriptorTest : TestCase() {

    fun testAbsolutePathProducesDapCommand() {
        val command = buildDapCommand("/usr/local/bin/geblang")
        assertEquals(listOf("/usr/local/bin/geblang", "dap"), command)
    }

    fun testBareNameProducesDapCommand() {
        val command = buildDapCommand("geblang")
        assertEquals(listOf("geblang", "dap"), command)
    }

    fun testDapIsAlwaysTheSecondArgument() {
        val command = buildDapCommand("geblang")
        assertEquals("dap", command[1])
    }

    fun testCommandHasExactlyTwoElements() {
        val command = buildDapCommand("/opt/geblang/bin/geblang")
        assertEquals(2, command.size)
    }
}
