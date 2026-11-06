// The Android app is its own Gradle project, as desktop/ is its own Go module (decision 0024).
// The server binary never links it. Dependencies come from Google's Maven repository and Maven
// Central only, at build time, and every artifact is pinned by gradle/verification-metadata.xml.
pluginManagement {
    repositories {
        google {
            content {
                includeGroupByRegex("com\\.android.*")
                includeGroupByRegex("com\\.google.*")
                includeGroupByRegex("androidx.*")
            }
        }
        mavenCentral()
        gradlePluginPortal()
    }
}

dependencyResolutionManagement {
    repositoriesMode.set(RepositoriesMode.FAIL_ON_PROJECT_REPOS)
    repositories {
        google {
            content {
                includeGroupByRegex("com\\.android.*")
                includeGroupByRegex("com\\.google.*")
                includeGroupByRegex("androidx.*")
            }
        }
        mavenCentral()
    }
}

rootProject.name = "tendshelf-android"

// shared: Kotlin with no Android imports. API paths and position maths live here from the start so an
// iPhone app can reuse them later (operator answer, 2026-11-05).
include(":shared")
include(":app")
