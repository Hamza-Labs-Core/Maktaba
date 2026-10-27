// Maktaba for Android TV / Google TV: the `app` module on the phone apps' shared `core`
// (apps/android/core). AGP 9 compiles Kotlin itself; Compose and serialization are per module.
plugins {
    alias(libs.plugins.android.application) apply false
    alias(libs.plugins.android.library) apply false
    alias(libs.plugins.kotlin.jvm) apply false
    alias(libs.plugins.kotlin.compose) apply false
    alias(libs.plugins.kotlin.serialization) apply false
}
