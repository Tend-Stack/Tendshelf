// No Android imports in this module, ever: it is the part an iOS app could reuse (Kotlin Multiplatform
// later). It holds API paths and position logic; JSON parsing and networking stay in the app until a
// multiplatform JSON library is approved.
plugins {
    alias(libs.plugins.kotlin.jvm)
}

kotlin {
    jvmToolchain(21)
}

dependencies {
    testImplementation(libs.kotlin.test)
    testImplementation(libs.kotlin.test.junit)
    testImplementation(libs.junit)
}
