package com.dwgebler.geblang.highlighting

import junit.framework.TestCase

/**
 * Unit tests for the pure [GeblangInterpolation.ranges] helper.
 *
 * These deliberately avoid any PSI/annotator/platform machinery - the helper
 * is a plain function over a [String], so a plain JUnit [TestCase] is
 * sufficient here (mirroring the style of [com.dwgebler.geblang.notification.GeblangExecutableTest]).
 */
class GeblangInterpolationTest : TestCase() {

    fun testSingleInterpolationInMiddleOfString() {
        val text = "\"a \${x} b\""
        val ranges = GeblangInterpolation.ranges(text)
        assertEquals(1, ranges.size)
        val r = ranges[0]
        assertEquals("\${x}", text.substring(r.first, r.last + 1))
    }

    fun testTwoAdjacentInterpolationsAreNonOverlappingAndInOrder() {
        val text = "\"\${a}\${b}\""
        val ranges = GeblangInterpolation.ranges(text)
        assertEquals(2, ranges.size)
        val (first, second) = ranges
        assertEquals("\${a}", text.substring(first.first, first.last + 1))
        assertEquals("\${b}", text.substring(second.first, second.last + 1))
        assertTrue("ranges must be in order and non-overlapping", first.last < second.first)
    }

    fun testNestedBracesMatchOuterClosingBrace() {
        val text = "\"\${ {k: v} }\""
        val ranges = GeblangInterpolation.ranges(text)
        assertEquals(1, ranges.size)
        val r = ranges[0]
        assertEquals("\${ {k: v} }", text.substring(r.first, r.last + 1))
    }

    fun testRawSingleQuotedStringYieldsNoRanges() {
        val text = "'\${x}'"
        val ranges = GeblangInterpolation.ranges(text)
        assertTrue(ranges.isEmpty())
    }

    fun testBareDollarWithoutBraceIsNotInterpolation() {
        val text = "\"cost is \$5\""
        val ranges = GeblangInterpolation.ranges(text)
        assertTrue(ranges.isEmpty())
    }

    fun testUnterminatedInterpolationYieldsNoRanges() {
        val text = "\"\${x\""
        val ranges = GeblangInterpolation.ranges(text)
        assertTrue(ranges.isEmpty())
    }

    fun testTripleDoubleQuotedStringFindsInterpolationRegardlessOfDelimiterPrefix() {
        val text = "\"\"\"\${x}\"\"\""
        val ranges = GeblangInterpolation.ranges(text)
        assertEquals(1, ranges.size)
        val r = ranges[0]
        assertEquals("\${x}", text.substring(r.first, r.last + 1))
    }

    fun testEmptyString() {
        val ranges = GeblangInterpolation.ranges("")
        assertTrue(ranges.isEmpty())
    }

    fun testNoDollarAtAll() {
        val text = "\"just a plain string\""
        val ranges = GeblangInterpolation.ranges(text)
        assertTrue(ranges.isEmpty())
    }
}
