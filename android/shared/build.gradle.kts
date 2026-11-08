// No Android imports in this module, ever: it is the part an iOS app could reuse. It is a Kotlin
// Multiplatform module that targets only the JVM today (jvm()); commonMain holds API paths, position logic
// and the strict URL rules, and the few JVM-only pieces sit behind expect/actual or in jvmMain (see
// "iOS later" in docs/android.md). The iosArm64()/iosSimulatorArm64() targets are added on a Mac, where
// they can link. JSON parsing and networking stay in the app until a multiplatform JSON library is approved.
plugins {
    alias(libs.plugins.kotlin.multiplatform)
}

kotlin {
    jvmToolchain(21)
    jvm()

    sourceSets {
        commonTest.dependencies {
            implementation(libs.kotlin.test)
        }
        jvmTest.dependencies {
            implementation(libs.kotlin.test.junit)
            implementation(libs.junit)
        }
    }
}
