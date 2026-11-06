plugins {
    alias(libs.plugins.android.application)
}

android {
    namespace = "app.tendshelf.android"
    compileSdk = 36

    defaultConfig {
        applicationId = "app.tendshelf.android"
        minSdk = 28 // Android Auto needs Android 9
        targetSdk = 36
        versionCode = 1
        versionName = "0.0.0-a0"
        // A0 only: the hard-coded server. The emulator reaches the host's loopback at 10.0.2.2.
        // A start intent may override it (extra "server"). Replaced by the connect page in A2.
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
}
