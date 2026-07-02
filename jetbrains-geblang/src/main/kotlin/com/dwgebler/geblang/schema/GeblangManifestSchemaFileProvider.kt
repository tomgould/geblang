package com.dwgebler.geblang.schema

import com.intellij.openapi.vfs.VirtualFile
import com.jetbrains.jsonSchema.extension.JsonSchemaFileProvider
import com.jetbrains.jsonSchema.extension.JsonSchemaProviderFactory
import com.jetbrains.jsonSchema.extension.SchemaType

/**
 * Maps the bundled JSON schema to any file named exactly `geblang.yaml`, giving
 * completion and validation for the Geblang package manifest.
 *
 * The manifest filename is fixed by the toolchain (see docs/user/07-modules-packages.md:
 * `geblang.yml` and `geblang.json` are also accepted by the CLI, but `geblang.yaml` is the
 * name used throughout the ecosystem, so matching is done purely on filename rather than
 * file type or location).
 */
class GeblangManifestSchemaFileProvider : JsonSchemaFileProvider {

    override fun isAvailable(file: VirtualFile): Boolean = file.name == MANIFEST_FILE_NAME

    override fun getName(): String = "Geblang Manifest"

    override fun getSchemaFile(): VirtualFile? =
        JsonSchemaProviderFactory.getResourceFile(
            GeblangManifestSchemaFileProvider::class.java,
            SCHEMA_RESOURCE_PATH
        )

    override fun getSchemaType(): SchemaType = SchemaType.embeddedSchema

    companion object {
        const val MANIFEST_FILE_NAME = "geblang.yaml"
        const val SCHEMA_RESOURCE_PATH = "/schemas/geblang-manifest.schema.json"
    }
}
