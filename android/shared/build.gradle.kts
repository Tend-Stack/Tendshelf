// No Android imports in this module, ever: it is the part an iOS app could reuse. It is a Kotlin
// Multiplatform module that targets only the JVM today (jvm()); commonMain holds API paths, position logic
// and the strict URL rules, and the few JVM-only pieces sit behind expect/actual or in jvmMain (see
// "iOS later" in docs/android.md). The iosArm64()/iosSimulatorArm64() targets are added on a Mac, where
// they can link. JSON reading and writing use kotlinx-serialization-json (commonMain only, operator 2026-11-08); networking stays in the app.
plugins {
    alias(libs.plugins.kotlin.multiplatform)
    alias(libs.plugins.kotlin.serialization)
}

kotlin {
    jvmToolchain(21)
    jvm()

    sourceSets {
        commonMain.dependencies {
            api(libs.kotlinx.serialization.json)
        }
        commonTest.dependencies {
            implementation(libs.kotlin.test)
        }
        jvmTest.dependencies {
            implementation(libs.kotlin.test.junit)
            implementation(libs.junit)
        }
    }
}
