// The native Android apps (Epic 42.3): Maktaba Player and Maktaba Remote, each for phone,
// foldable and tablet, on a shared `core` (the API client, sign-in, token storage, the
// stream's authorization, the theme and the fold-aware layout). AGP 9 compiles Kotlin
// itself; the Compose and serialization compiler plugins are applied per module.
plugins {
    alias(libs.plugins.android.application) apply false
    alias(libs.plugins.android.library) apply false
    alias(libs.plugins.kotlin.compose) apply false
    alias(libs.plugins.kotlin.serialization) apply false
}
