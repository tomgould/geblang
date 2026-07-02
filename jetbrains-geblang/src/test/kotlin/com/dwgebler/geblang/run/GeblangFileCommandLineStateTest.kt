package com.dwgebler.geblang.run

import junit.framework.TestCase

/**
 * Unit tests for [buildRunArguments], the pure argument-construction helper used by
 * [GeblangFileCommandLineState]. No IDE fixtures needed - plain values in, plain
 * list out.
 */
class GeblangFileCommandLineStateTest : TestCase() {

    fun testFileTargetWithoutArgumentsProducesBaseArguments() {
        val args = buildRunArguments("scripts/main.gb", "")
        assertEquals(listOf("run", "scripts/main.gb"), args)
    }

    fun testBlankArgumentsAreTreatedAsAbsent() {
        val args = buildRunArguments("scripts/main.gb", "   ")
        assertEquals(listOf("run", "scripts/main.gb"), args)
    }

    fun testSingleArgumentIsAppendedAfterTarget() {
        val args = buildRunArguments("scripts/main.gb", "--verbose")
        assertEquals(listOf("run", "scripts/main.gb", "--verbose"), args)
    }

    fun testMultipleWhitespaceSeparatedArgumentsAreAppendedInOrder() {
        val args = buildRunArguments("scripts/main.gb", "--verbose --seed 42")
        assertEquals(listOf("run", "scripts/main.gb", "--verbose", "--seed", "42"), args)
    }

    fun testExtraWhitespaceBetweenArgumentsIsCollapsed() {
        val args = buildRunArguments("scripts/main.gb", "  --verbose   --seed  42  ")
        assertEquals(listOf("run", "scripts/main.gb", "--verbose", "--seed", "42"), args)
    }

    fun testTargetIsAlwaysTheSecondArgument() {
        val args = buildRunArguments("scripts/main.gb", "--flag")
        assertEquals("scripts/main.gb", args[1])
    }
}
