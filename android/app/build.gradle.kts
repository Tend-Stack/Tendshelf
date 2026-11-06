plugins {
    alias(libs.plugins.android.application)
}

// The bundled start and offline pages use the server's own stylesheets and fonts. They are copied from
// internal/web/static at build time into a generated folder, never into git, so the two cannot drift.
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
        versionCode = 1
        versionName = "0.0.0-a2"
        // Still read by PlaybackService (A3 owns it and drops it). MainActivity no longer uses it: the server is chosen on
        // the connect page.
        buildConfigField("String", "DEFAULT_SERVER", "\"http://10.0.2.2:8097\"")
    }

    buildFeatures {
        buildConfig = true
    }

    buildTypes {
        release {
            isMinifyEnabled = false
        }
    }
}

dependencies {
    implementation(project(":shared"))
    implementation(libs.media3.exoplayer)
    implementation(libs.media3.session)
    implementation(libs.media3.datasource)
    implementation(libs.androidx.webkit)
}
