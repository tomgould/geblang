package com.dwgebler.geblang.schema

import com.intellij.openapi.project.Project
import com.jetbrains.jsonSchema.extension.JsonSchemaFileProvider
import com.jetbrains.jsonSchema.extension.JsonSchemaProviderFactory

/**
 * Registers [GeblangManifestSchemaFileProvider] with the platform's JSON schema
 * service. Registered in plugin.xml under the `JavaScript.JsonSchema.ProviderFactory`
 * extension point (contributed by the bundled `com.intellij.modules.json` module).
 */
class GeblangManifestSchemaProviderFactory : JsonSchemaProviderFactory {

    override fun getProviders(project: Project): List<JsonSchemaFileProvider> =
        listOf(GeblangManifestSchemaFileProvider())
}
