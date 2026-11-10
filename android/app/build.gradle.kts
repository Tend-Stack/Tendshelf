plugins {
    alias(libs.plugins.android.application)
}

// The bundled start and offline pages use the server's own stylesheets and fonts. They are copied from
// internal/web/static at build time into a generated folder, never into git, so the two cannot drift.
// Version (plan A10 D2): `-PtsVersion=2026.3.0` from the Git tag. versionCode = year * 100000 + release * 1000 + fix, so
// it rises with every release and fits an Int for centuries. Without the property this is a development build.
fun tsVersionCode(v: String): Int {
    val m = Regex("""(\d{4})\.(\d{1,2})\.(\d{1,3})""").matchEntire(v)
        ?: throw GradleException("tsVersion must be YEAR.RELEASE.FIX, for example 2026.3.0; got '$v'")
    val (year, release, fix) = m.destructured.toList().map { it.toInt() }
    if (release < 1 || release > 99 || fix > 999) throw GradleException("tsVersion out of range (release 1..99, fix 0..999): $v")
    return year * 100000 + release * 1000 + fix
}

val tsVersion: String? = providers.gradleProperty("tsVersion").orNull
val tsVersionName = tsVersion ?: "0.0.0-dev"
val tsVersionCodeValue = if (tsVersion != null) tsVersionCode(tsVersion) else 1

tasks.register("printVersionCode") {
    val code = tsVersionCodeValue
    doLast { println("versionCode=$code") }
}

// Signing (plan A10 D1): only from the environment, only when all four are set. Never from a file in the repository.
val tsSigningNames = listOf("TS_ANDROID_KEYSTORE", "TS_ANDROID_KEYSTORE_PASSWORD", "TS_ANDROID_KEY_ALIAS", "TS_ANDROID_KEY_PASSWORD")
val tsSigning = tsSigningNames.associateWith { System.getenv(it).orEmpty() }
val tsSigned = tsSigning.values.all { it.isNotEmpty() }

abstract class CopyShellAssets : DefaultTask() {
    @get:InputDirectory
    @get:PathSensitive(PathSensitivity.RELATIVE)
    abstract val staticDir: DirectoryProperty

    @get:OutputDirectory
    abstract val outputDir: DirectoryProperty

    @TaskAction
    fun copyAll() {
        val src = staticDir.get().asFile
        val out = outputDir.get().asFile
        out.deleteRecursively()
        File(out, "fonts").mkdirs()
        for (name in listOf("tokens.css", "bundle.css")) File(src, "css/$name").copyTo(File(out, name))
        // fonts.css names /static/fonts/...; the bundled origin serves them from /assets/fonts/.
        File(out, "fonts.css").writeText(File(src, "css/fonts.css").readText().replace("/static/fonts/", "/assets/fonts/"))
        File(src, "fonts").listFiles { f -> f.extension == "woff2" }!!.forEach { it.copyTo(File(out, "fonts/${it.name}")) }
    }
}

val copyShellAssets = tasks.register<CopyShellAssets>("copyShellAssets") {
    staticDir.set(rootProject.layout.projectDirectory.dir("../internal/web/static"))
    outputDir.set(layout.buildDirectory.dir("generated/shellAssets"))
}

androidComponents {
    onVariants { variant ->
        variant.sources.assets?.addGeneratedSourceDirectory(copyShellAssets, CopyShellAssets::outputDir)
    }
}

android {
    namespace = "app.tendshelf.android"
    compileSdk = 36

    defaultConfig {
        applicationId = "app.tendshelf.android"
        minSdk = 28 // Android Auto needs Android 9
        targetSdk = 36
        versionCode = tsVersionCodeValue
        versionName = tsVersionName
    }

    // The signing key is the person's own; nothing here creates or stores one.
    if (tsSigned) {
        signingConfigs.create("release") {
            storeFile = file(tsSigning.getValue("TS_ANDROID_KEYSTORE"))
            storePassword = tsSigning.getValue("TS_ANDROID_KEYSTORE_PASSWORD")
            keyAlias = tsSigning.getValue("TS_ANDROID_KEY_ALIAS")
            keyPassword = tsSigning.getValue("TS_ANDROID_KEY_PASSWORD")
        }
    }

    // Reproducible bytes (D3): no dependency block signed by Google, no Git state in the APK.
    dependenciesInfo {
        includeInApk = false
        includeInBundle = false
    }
    buildToolsVersion = "36.0.0"

    buildFeatures {
        buildConfig = true
    }

    // NewApi is fatal: minSdk is 28, and a call from a newer API level is a crash on the phones below it (readNBytes was one).
    // lint-baseline.xml holds only the older findings of other kinds (Media3 opt-in marks, the back-gesture migration), so
    // lintRelease passes today and fails on anything new. Nothing of kind NewApi may enter the baseline.
    lint {
        fatal += "NewApi"
        abortOnError = true
        baseline = file("lint-baseline.xml")
    }

    buildTypes {
        release {
            isMinifyEnabled = false
            vcsInfo.include = false
            // Never the debug key: without all four variables the APK stays unsigned (app-release-unsigned.apk).
            if (tsSigned) signingConfig = signingConfigs.getByName("release")
        }
    }
}

dependencies {
    implementation(project(":shared"))
    implementation(libs.media3.exoplayer)
    implementation(libs.media3.session)
    implementation(libs.media3.datasource)
    implementation(libs.androidx.webkit)
    implementation(libs.androidx.core)
    implementation(libs.androidx.work)
}
