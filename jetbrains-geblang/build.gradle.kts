import org.jetbrains.intellij.platform.gradle.IntelliJPlatformType
import org.jetbrains.intellij.platform.gradle.TestFrameworkType

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

        // Test framework — required explicitly since IPGP 2.x (no longer resolved
        // implicitly at task runtime). TestFrameworkType.Platform pulls in the base
        // platform test infra needed for LexerTestCase / UsefulTestCase / TestCase.
        testFramework(TestFrameworkType.Platform)
    }

    // JUnit4 is no longer bundled via the IntelliJ Platform test framework in IPGP 2.x —
    // must be declared explicitly. LexerTestCase extends JUnit3/4-style TestCase.
    testImplementation("junit:junit:4.13.2")

    // Workaround for JetBrains IJPL-157292: opentest4j is not resolved transitively
    // by TestFrameworkType.Platform, which otherwise causes
    // NoClassDefFoundError: org/opentest4j/AssertionFailedError at test runtime.
    testImplementation("org.opentest4j:opentest4j:1.3.0")
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
