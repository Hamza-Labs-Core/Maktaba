// Maktaba Player for Windows and Linux (Epic 42.4): Compose for Desktop, on the apps' portable
// client (`apps/android/client`, the same code the Android apps run). Its own build, like the
// Android TV app's: no Android plugin or SDK is needed to build it.
pluginManagement {
    repositories {
        mavenCentral()
        google()
        gradlePluginPortal()
    }
}

dependencyResolutionManagement {
    repositoriesMode.set(RepositoriesMode.FAIL_ON_PROJECT_REPOS)
    repositories {
        mavenCentral()
        google()
    }
    versionCatalogs {
        create("libs") { from(files("../android/gradle/libs.versions.toml")) }
        create("desk") { from(files("gradle/desktop.versions.toml")) }
    }
}

rootProject.name = "MaktabaPlayer"
include(":client")
project(":client").projectDir = file("../android/client")
