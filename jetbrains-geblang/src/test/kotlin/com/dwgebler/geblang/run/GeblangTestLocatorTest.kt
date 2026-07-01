package com.dwgebler.geblang.run

import junit.framework.TestCase

/**
 * Unit tests for [GeblangTestLocator.parsePath], the pure URL-parsing helper.
 * Deliberately avoids [GeblangTestLocator.getLocation] itself, which needs a live
 * [com.intellij.openapi.project.Project] and PSI/VFS - that path is exercised only
 * under a running IDE (see manual test plan in README.md).
 */
class GeblangTestLocatorTest : TestCase() {

    fun testClassOnlyPathParsesToClassNameWithNullMethod() {
        val result = GeblangTestLocator.parsePath("Foo")
        assertEquals(GeblangTestLocation("Foo", null), result)
    }

    fun testClassAndMethodPathParsesBothParts() {
        val result = GeblangTestLocator.parsePath("Foo/bar")
        assertEquals(GeblangTestLocation("Foo", "bar"), result)
    }

    fun testNestedSeparatorOnlySplitsOnFirstSlash() {
        // Method names cannot legally contain '/', but the parser should still
        // behave predictably (first slash wins) rather than throwing.
        val result = GeblangTestLocator.parsePath("Foo/bar/baz")
        assertEquals(GeblangTestLocation("Foo", "bar/baz"), result)
    }

    fun testEmptyPathIsMalformed() {
        val result = GeblangTestLocator.parsePath("")
        assertNull(result)
    }

    fun testPathStartingWithSeparatorHasBlankClassNameAndIsMalformed() {
        val result = GeblangTestLocator.parsePath("/bar")
        assertNull(result)
    }

    fun testTrailingSeparatorWithNoMethodNameYieldsNullMethod() {
        val result = GeblangTestLocator.parsePath("Foo/")
        assertEquals(GeblangTestLocation("Foo", null), result)
    }
}
