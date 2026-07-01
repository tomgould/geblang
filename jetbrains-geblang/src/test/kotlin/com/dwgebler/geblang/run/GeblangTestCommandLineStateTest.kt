package com.dwgebler.geblang.run

import junit.framework.TestCase

/**
 * Unit tests for [buildTestArguments], the pure argument-construction helper used by
 * [GeblangTestCommandLineState]. No IDE fixtures needed - plain values in, plain
 * list out.
 */
class GeblangTestCommandLineStateTest : TestCase() {

    fun testFileTargetWithoutTagProducesBaseArguments() {
        val args = buildTestArguments("tests/user_test.gb", "")
        assertEquals(
            listOf("test", "--format", "teamcity", "tests/user_test.gb"),
            args
        )
    }

    fun testDirectoryTargetWithoutTagProducesBaseArguments() {
        val args = buildTestArguments("tests/", "")
        assertEquals(
            listOf("test", "--format", "teamcity", "tests/"),
            args
        )
    }

    fun testTagIsInsertedBeforeTargetWhenPresent() {
        val args = buildTestArguments("tests/", "integration")
        assertEquals(
            listOf("test", "--format", "teamcity", "--tag", "integration", "tests/"),
            args
        )
    }

    fun testBlankTagIsTreatedAsAbsent() {
        val args = buildTestArguments("tests/user_test.gb", "   ")
        assertEquals(
            listOf("test", "--format", "teamcity", "tests/user_test.gb"),
            args
        )
    }

    fun testTargetIsAlwaysTheLastArgument() {
        val args = buildTestArguments("tests/user_test.gb", "fast")
        assertEquals("tests/user_test.gb", args.last())
    }
}
