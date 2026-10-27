// Maktaba Player for Windows and Linux (Epic 42.4): browse and watch a home, nothing else.
// Compose for Desktop on the apps' portable client (`:client`, shared with the Android apps);
// JavaFX plays the video (H.264 and AAC, as MP4 or HLS: the streaming service's
// `desktop-player` profile). Installers: `packageReleaseMsi` (Windows), `packageReleaseDeb`
// and `packageReleaseRpm` (Linux), named and signed by .github/workflows/desktop-release.yml.
import org.gradle.internal.os.OperatingSystem
import org.jetbrains.compose.desktop.application.dsl.TargetFormat
import org.jetbrains.kotlin.gradle.dsl.JvmTarget

plugins {
    alias(libs.plugins.kotlin.jvm)
    alias(libs.plugins.kotlin.compose)
    alias(libs.plugins.kotlin.serialization)
    alias(desk.plugins.compose)
}

/** The release (`-Pmaktaba.version=0.3.34`, from the tag), else a development build. */
val appVersion: String = (findProperty("maktaba.version") as String?)?.removePrefix("v") ?: "0.0.0-dev"

/** The installers take a numeric version only: a pre-release's core (0.3.34-rc.1 → 0.3.34). */
val installerVersion: String = appVersion.substringBefore('-').substringBefore('+')
    .let { if (Regex("""\d+\.\d+\.\d+""").matches(it)) it else "1.0.0" }

/** JavaFX's native parts for the machine that builds (each release leg builds on its own OS). */
val javafxPlatform: String = run {
    val os = OperatingSystem.current()
    val arm = System.getProperty("os.arch").let { it == "aarch64" || it == "arm64" }
    when {
        os.isWindows -> "win"
        os.isMacOsX -> if (arm) "mac-aarch64" else "mac"
        else -> if (arm) "linux-aarch64" else "linux"
    }
}

java {
    sourceCompatibility = JavaVersion.VERSION_17
    targetCompatibility = JavaVersion.VERSION_17
}

kotlin {
    compilerOptions { jvmTarget.set(JvmTarget.JVM_17) }
}

dependencies {
    implementation(project(":client"))
    implementation(compose.desktop.currentOs)
    implementation(desk.compose.material3)
    implementation(desk.compose.icons)
    implementation(libs.zxing.core)
    implementation(desk.jmdns)
    implementation(desk.jna.platform)
    implementation(libs.kotlinx.coroutines.core)
    // Dispatchers.Main on the Swing event thread
    implementation("org.jetbrains.kotlinx:kotlinx-coroutines-swing:${libs.versions.coroutines.get()}")
    for (module in listOf("base", "graphics", "media", "swing")) {
        implementation("org.openjfx:javafx-$module:${desk.versions.javafx.get()}:$javafxPlatform")
    }

    testImplementation(testFixtures(project(":client")))
    testImplementation(libs.junit)
    testImplementation(libs.okhttp.mockwebserver)
    testImplementation(libs.kotlinx.coroutines.test)
}

// The version the app shows and compares with the update feed's
val writeBuildInfo = tasks.register("writeBuildInfo") {
    val out = layout.buildDirectory.dir("generated/buildinfo")
    val version = appVersion
    // the apps' updater key (the repository variable TAURI_UPDATER_PUBKEY): updates are checked against it
    val pubkey = (findProperty("maktaba.updaterPubkey") as String?)?.trim().orEmpty()
    require(pubkey.all { it.isLetterOrDigit() || it in "+/=" }) { "maktaba.updaterPubkey must be base64" }
    inputs.property("version", version)
    inputs.property("pubkey", pubkey)
    outputs.dir(out)
    doLast {
        val f = out.get().file("com/hamzalabs/maktaba/desktop/BuildInfo.kt").asFile
        f.parentFile.mkdirs()
        f.writeText(
            """
            |package com.hamzalabs.maktaba.desktop
            |
            |/** Written by the build (build.gradle.kts). */
            |object BuildInfo {
            |    const val VERSION = "$version"
            |    const val UPDATER_PUBKEY = "$pubkey"
            |}
            |""".trimMargin(),
        )
    }
}
sourceSets.main {
    kotlin.srcDir(writeBuildInfo)
    // the apps' fonts (IBM Plex Sans Arabic, Amiri), from the Android core rather than a copy
    resources.srcDir("../android/core/src/main/res/font")
}

compose.desktop {
    application {
        mainClass = "com.hamzalabs.maktaba.desktop.MainKt"
        jvmArgs += listOf("-Dfile.encoding=UTF-8")
        nativeDistributions {
            targetFormats(TargetFormat.Msi, TargetFormat.Deb, TargetFormat.Rpm)
            packageName = "maktaba-player"
            packageVersion = installerVersion
            description = "Maktaba Player: watch your Maktaba home"
            vendor = "Hamza Labs"
            copyright = "© Hamza Labs"
            // JavaFX on the class path, the stream proxy's HTTP server, TLS: the whole runtime
            includeAllModules = true
            windows {
                // its own identity, apart from the server app (com.hamzalabs.maktaba)
                upgradeUuid = "5d1f6a0c-7c43-4f7e-9f9a-3b0f2a8e4c42"
                menuGroup = "Maktaba"
                // a current-user install: the player updates itself without an administrator
                perUserInstall = true
                shortcut = true
                menu = true
                iconFile.set(project.file("icons/maktaba-player.ico"))
            }
            linux {
                packageName = "maktaba-player"
                debMaintainer = "Hamza Labs <apps@hamzalabs.com>"
                menuGroup = "AudioVideo"
                appCategory = "video"
                shortcut = true
                iconFile.set(project.file("icons/maktaba-player.png"))
            }
        }
    }
}
