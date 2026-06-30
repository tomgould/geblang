import org.jetbrains.intellij.platform.gradle.IntelliJPlatformType

plugins {
    id("java")
    id("org.jetbrains.kotlin.jvm") version "1.9.25"
    id("org.jetbrains.intellij.platform") version "2.2.1"
}

group = "com.dwgebler"
version = "0.1.0"

kotlin {
    jvmToolchain(17)
}

repositories {
    mavenCentral()
    intellijPlatform {
        defaultRepositories()
    }
}

dependencies {
    intellijPlatform {
        intellijIdeaCommunity("2024.2.4")
        instrumentationTools()
        pluginVerifier()
        // LSP4IJ from JetBrains Marketplace — provides LSP client infra
        plugin("com.redhat.devtools.lsp4ij", "0.20.1")
    }
}

intellijPlatform {
    pluginConfiguration {
        name = "Geblang"
        version = "0.1.0"

        ideaVersion {
            sinceBuild = "242"
            untilBuild = "243.*"
        }
    }

    pluginVerification {
        ides {
            ide(IntelliJPlatformType.IntellijIdeaCommunity, "2024.2.4")
        }
    }

    signing {
        // Not signing for local trial — no certificate configured
    }

    publishing {
        // Not publishing — local trial only
    }
}
