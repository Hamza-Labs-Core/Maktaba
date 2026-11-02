// The native Android apps (Epic 42.3): Maktaba Player and Maktaba Remote, each for phone,
// foldable and tablet, on a shared `core` (the theme, the fold-aware layout, the home picker and
// sign-in screens), itself on the portable `client` (the API client, sign-in, token sealing,
// the stream's authorization), which the desktop player shares. AGP 9 compiles Kotlin
// itself; the Compose and serialization compiler plugins are applied per module.
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
