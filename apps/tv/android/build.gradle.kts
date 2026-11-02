// Maktaba for Android TV / Google TV: the `app` module on the phone apps' shared `core`
// (apps/android/core). AGP 9 compiles Kotlin itself; Compose and serialization are per module.
plugins {
    alias(libs.plugins.android.application) apply false
    alias(libs.plugins.android.library) apply false
    alias(libs.plugins.kotlin.jvm) apply false
    alias(libs.plugins.kotlin.compose) apply false
    alias(libs.plugins.kotlin.serialization) apply false
}

// Every JVM test keeps the app's own log (android.util.Log, AppLog): Robolectric drops it unless
// asked, and a test that fails on a runner is then read from its report with no clue why. The log
// goes to the test's standard output, which the XML report and the HTML report keep.
subprojects {
    tasks.withType<Test>().configureEach {
        systemProperty("robolectric.logging", "stdout")
    }
}
