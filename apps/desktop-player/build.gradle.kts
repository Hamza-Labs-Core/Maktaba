// Maktaba Player for Windows and Linux (Epic 42.4, Epic 55): the Android and Apple Players' sections.
// Compose for Desktop on the apps' portable client (`:client`, shared with the Android apps);
// JavaFX plays the video (H.264 and AAC, as MP4 or HLS: the streaming service's
// `desktop-player` profile). Installers: `packageMsi` (Windows), `packageDeb` and `packageRpm`
// (Linux), named and signed by .github/workflows/desktop-release.yml. Not the `packageRelease*`
// tasks: those run ProGuard, and the player's crash frames are read at Maktaba as they come
// (Epic 53 story 53.16; tools/ci/tests/test_desktop_player_release.py keeps it so).
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
    implementation(desk.jewel.decorated.window)
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
    // screen tests off screen (runComposeUiTest: no window, no display)
    testImplementation(compose.desktop.uiTestJUnit4)
}

// Story 13.14: the .msi in Maktaba's look, and its Arabic. compose hands jpackage a resource
// folder it empties before each run, so the WiX bitmaps, the text styles and the Arabic
// transform go into the built .msi instead, through Windows Installer's API, before anything
// signs it: tools/release/msi/theme_msi.py (Python, which windows-latest has; another with
// -Pmaktaba.python=<program>). CI's Player job and the release legs check it with --check.
val msiDir = layout.buildDirectory.dir("compose/binaries/main/msi")
val themeMsi = rootProject.file("../../tools/release/msi/theme_msi.py")
val python = (findProperty("maktaba.python") as String?) ?: "python"
tasks.matching { it.name == "packageMsi" }.configureEach {
    inputs.files(fileTree(rootProject.file("../../tools/release/msi")), fileTree(project.file("msi")))
        .withPropertyName("msiTheme")
    doLast {
        val msis = msiDir.get().asFile.listFiles { f -> f.name.endsWith(".msi") }.orEmpty()
        check(msis.size == 1) { "want one .msi in ${msiDir.get().asFile}, found ${msis.map { it.name }}" }
        // its words into the build's log (a daemon's own stdout reaches no console)
        val proc = ProcessBuilder(python, themeMsi.absolutePath, msis[0].absolutePath).redirectErrorStream(true).start()
        proc.inputStream.bufferedReader().forEachLine { println(it) }
        check(proc.waitFor() == 0) { "theme_msi.py failed on ${msis[0].name}" }
    }
}

// The version the app shows and compares with the update feed's
val writeBuildInfo = tasks.register("writeBuildInfo") {
    val out = layout.buildDirectory.dir("generated/buildinfo")
    val version = appVersion
    // the apps' updater key (the repository variable TAURI_UPDATER_PUBKEY): updates are checked against it
    val pubkey = (findProperty("maktaba.updaterPubkey") as String?)?.trim().orEmpty()
    require(pubkey.all { it.isLetterOrDigit() || it in "+/=" }) { "maktaba.updaterPubkey must be base64" }
    // story 51.11's gate (issue #638): -Pmaktaba.capabilitiesGate=off builds a Player that shows
    // every feature; nothing (a release) leaves it on
    val gate = (findProperty("maktaba.capabilitiesGate") as String?)?.trim().orEmpty()
    require(gate.all { it.isLetterOrDigit() }) { "maktaba.capabilitiesGate must be letters and digits" }
    inputs.property("version", version)
    inputs.property("pubkey", pubkey)
    inputs.property("capabilitiesGate", gate)
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
            |    const val CAPABILITIES_GATE = "$gate"
            |}
            |""".trimMargin(),
        )
    }
}
sourceSets.main {
    kotlin.srcDir(writeBuildInfo)
    // the apps' fonts (IBM Plex Sans Arabic, Amiri), from the Android core rather than a copy
    resources.srcDir("../android/core/src/main/res/font")
    // story 13.15: the app's icon, in the title bar
    resources.srcDir("icons")
}

compose.desktop {
    application {
        mainClass = "com.hamzalabs.maktaba.desktop.MainKt"
        // story 13.15: the installers' runtime is jlinked from the JetBrains Runtime (jbr.sh),
        // whose custom title bar the Player's own frame needs; without the property (a local
        // build, the tests) the build's JDK, and the Player falls back to the system's frame
        providers.gradleProperty("maktaba.jbr").orNull?.let { javaHome = it }
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
            // story 13.18: the libmpv the Player carries, per system (resources/windows-x64,
            // linux-x64, linux-arm64), put there by libmpv/build.sh in CI and the release; the
            // Player finds it through compose.application.resources.dir
            appResourcesRootDir.set(project.layout.projectDirectory.dir("resources"))
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

// Story 13.18: libmpv (in the app's resources, which jpackage does not read for dependencies)
// uses the system's libass, libva, PulseAudio and ALSA client libraries: the .deb and the .rpm
// depend on them by their package names (no spaces: compose writes the args file one argument
// to a line, and jpackage reads a space as a separator)
val libmpvDebDeps = "libass9,libva2,libva-drm2,libpulse0,libasound2"
val libmpvRpmDeps = "libass,libva,pulseaudio-libs,alsa-lib"
tasks.withType<org.jetbrains.compose.desktop.application.tasks.AbstractJPackageTask>().configureEach {
    when {
        name.endsWith("Deb") -> freeArgs.addAll("--linux-package-deps", libmpvDebDeps)
        name.endsWith("Rpm") -> freeArgs.addAll("--linux-package-deps", libmpvRpmDeps)
    }
}
