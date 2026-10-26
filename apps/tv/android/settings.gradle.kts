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
        google()
        mavenCentral()
    }
    // The phone apps' catalog (one set of versions for both builds) and the TV's own extras.
    versionCatalogs {
        create("libs") { from(files("../../android/gradle/libs.versions.toml")) }
        create("tv") { from(files("gradle/tv.versions.toml")) }
    }
}

rootProject.name = "MaktabaTV"
include(":app")
// The phone apps' shared core (Epic 42.3): the API client, sign-in, Keystore token storage, the
// stream's authorization, finding homes on the network. Built from its own folder, not copied.
include(":core")
project(":core").projectDir = file("../../android/core")
