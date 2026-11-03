// Maktaba Player for Windows and Linux (Epic 42.4): Compose for Desktop, on the apps' portable
// client (`apps/android/client`, the same code the Android apps run). Its own build, like the
// Android TV app's: no Android plugin or SDK is needed to build it.
// Each artifact is asked of the repository that publishes it: Google's only for the Android and
// AndroidX groups (the androidx JVM libraries Compose for Desktop uses), so Kotlin and JetBrains
// artifacts come from Maven Central, as the Android builds' settings do.
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
        mavenCentral()
        google {
            content {
                includeGroupByRegex("com\\.android.*")
                includeGroupByRegex("com\\.google.*")
                includeGroupByRegex("androidx.*")
            }
        }
    }
    versionCatalogs {
        create("libs") { from(files("../android/gradle/libs.versions.toml")) }
        create("desk") { from(files("gradle/desktop.versions.toml")) }
    }
}

rootProject.name = "MaktabaPlayer"
include(":client")
project(":client").projectDir = file("../android/client")
