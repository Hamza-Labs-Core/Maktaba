// Root Gradle config for the Maktaba Android TV app. Plugins are
// declared (not applied) here and applied per-module; versions come
// from the `gradle/libs.versions.toml` version catalog.
plugins {
    alias(libs.plugins.android.application) apply false
    alias(libs.plugins.kotlin.android) apply false
    alias(libs.plugins.kotlin.serialization) apply false
}
