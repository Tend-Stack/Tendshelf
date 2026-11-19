// No Android imports in this module, ever: it is the part the iPhone app reuses (decision 0048). It is a Kotlin
// Multiplatform module: jvm() for Android, and iosArm64()/iosSimulatorArm64() packed into one static XCFramework
// "TendshelfShared", declared only on a Mac (os.name starts with "Mac"), so Linux and CI never configure an iOS target
// and never need the Kotlin/Native toolchain. commonMain holds API paths, position logic and the strict URL rules; the
// few platform pieces sit behind expect/actual (RawUri) or in jvmMain/iosMain. No java.* or javax.* import and no
// Math.* in commonMain (android/tools/check-android.sh enforces it). JSON reading and writing use
// kotlinx-serialization-json (commonMain only, operator 2026-11-08); networking stays in the apps. See docs/ios.md.
import org.jetbrains.kotlin.gradle.plugin.mpp.apple.XCFramework

plugins {
    alias(libs.plugins.kotlin.multiplatform)
    alias(libs.plugins.kotlin.serialization)
}

kotlin {
    jvmToolchain(21)
    jvm()

    // SynchronizedOnJvm is an optional-expectation annotation (an expect annotation class is still Beta).
    compilerOptions.freeCompilerArgs.add("-Xexpect-actual-classes")

    if (System.getProperty("os.name").startsWith("Mac")) {
        val xcf = XCFramework("TendshelfShared")
        listOf(iosArm64(), iosSimulatorArm64()).forEach {
            it.binaries.framework {
                baseName = "TendshelfShared"
                isStatic = true
                xcf.add(this)
            }
        }
    }

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

// The simulator the iOS unit tests boot: a UDID or a name (ios/tools/ios-build.sh passes the UDID).
tasks.withType<org.jetbrains.kotlin.gradle.targets.native.tasks.KotlinNativeSimulatorTest>().configureEach {
    device.set(providers.gradleProperty("tendshelf.iosSimulator").orElse("iPhone 17"))
}
