// The apps' portable client (Epic 42.4): the API client, sign-in, token sealing, the home
// picker's rules and the stream's authorization, in plain Kotlin on the JVM. The Android
// `core` (phone, foldable, tablet, Android TV) and the desktop player (Compose for
// Desktop, `apps/desktop-player`) are both built on it, so neither has its own copy.
import org.jetbrains.kotlin.gradle.dsl.JvmTarget

plugins {
    alias(libs.plugins.kotlin.jvm)
    alias(libs.plugins.kotlin.serialization)
    `java-test-fixtures`
}

java {
    sourceCompatibility = JavaVersion.VERSION_17
    targetCompatibility = JavaVersion.VERSION_17
}

kotlin {
    compilerOptions { jvmTarget.set(JvmTarget.JVM_17) }
}

dependencies {
    api(libs.okhttp)
    api(libs.kotlinx.serialization.json)
    api(libs.kotlinx.coroutines.core)

    // MemoryTokens, MapStore, jwt(): the tests of every module built on the client
    testFixturesApi(libs.okhttp)

    testImplementation(libs.junit)
    testImplementation(libs.okhttp.mockwebserver)
    testImplementation(libs.kotlinx.coroutines.test)
}
