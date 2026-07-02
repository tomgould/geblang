package com.dwgebler.geblang.schema

import com.google.gson.JsonParser
import com.intellij.testFramework.fixtures.BasePlatformTestCase
import com.jetbrains.jsonSchema.extension.SchemaType

/**
 * Verifies the geblang.yaml manifest schema is wired up correctly:
 * - [GeblangManifestSchemaFileProvider] matches a file named exactly `geblang.yaml`
 *   and rejects any other filename.
 * - The bundled schema resource loads as a [com.intellij.openapi.vfs.VirtualFile]
 *   and its contents parse as valid JSON.
 */
class GeblangManifestSchemaFileProviderTest : BasePlatformTestCase() {

    private val provider = GeblangManifestSchemaFileProvider()

    fun testIsAvailableForGeblangYaml() {
        val file = myFixture.configureByText("geblang.yaml", "name: acme.tools\n").virtualFile
        assertTrue(provider.isAvailable(file))
    }

    fun testIsNotAvailableForOtherYaml() {
        val file = myFixture.configureByText("other.yaml", "name: acme.tools\n").virtualFile
        assertFalse(provider.isAvailable(file))
    }

    fun testSchemaTypeIsEmbedded() {
        assertEquals(SchemaType.embeddedSchema, provider.schemaType)
    }

    fun testBundledSchemaResourceLoadsAndIsValidJson() {
        val schemaFile = provider.schemaFile
        assertNotNull("bundled schema resource should resolve to a VirtualFile", schemaFile)

        val text = String(schemaFile!!.contentsToByteArray(), Charsets.UTF_8)
        val parsed = JsonParser.parseString(text)
        assertTrue("schema resource should parse as a JSON object", parsed.isJsonObject)

        val obj = parsed.asJsonObject
        assertTrue(obj.has("properties"))
        assertTrue(obj.get("properties").asJsonObject.has("name"))
        assertTrue(obj.get("properties").asJsonObject.has("dependencies"))
    }
}
