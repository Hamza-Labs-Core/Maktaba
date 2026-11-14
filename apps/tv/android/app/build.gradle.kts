import com.android.build.api.artifact.SingleArtifact
import org.gradle.api.artifacts.component.ModuleComponentIdentifier
import org.gradle.api.artifacts.result.ResolvedComponentResult
import org.gradle.api.artifacts.result.ResolvedDependencyResult
import javax.xml.parsers.DocumentBuilderFactory
import org.w3c.dom.Element

plugins {
    alias(libs.plugins.android.application)
    alias(libs.plugins.kotlin.compose)
    alias(libs.plugins.kotlin.serialization)
    // screenshot tests on the JVM (Robolectric): recordRoborazziGoogleDebug writes the references,
    // verifyRoborazziGoogleDebug (CI) fails when a screen no longer matches them
    alias(libs.plugins.roborazzi)
}

android {
    namespace = "com.hamzalabs.maktaba.tv"
    compileSdk = 37

    defaultConfig {
        applicationId = "com.hamzalabs.maktaba.tv"
        // Android 9: the practical floor for current Android TV / Google TV devices.
        minSdk = 28
        targetSdk = 37
        versionCode = providers.gradleProperty("maktaba.versionCode").orNull?.toInt() ?: 2
        versionName = providers.gradleProperty("maktaba.versionName").orNull ?: "0.2.0"
        // story 51.11's build switch, read by capabilitiesGate(): on unless -Pmaktaba.capabilitiesGate=off
        val capabilitiesGate = providers.gradleProperty("maktaba.capabilitiesGate").orNull.orEmpty()
        require(capabilitiesGate.all { it.isLetterOrDigit() }) { "maktaba.capabilitiesGate is on or off" }
        buildConfigField("String", "CAPABILITIES_GATE", "\"$capabilitiesGate\"")
        testInstrumentationRunner = "androidx.test.runner.AndroidJUnitRunner"
    }

    // One app, two stores (story 58.05). `google` is the Google Play build for Android TV and
    // Google TV; `amazon` is the Amazon Appstore build for Fire TV on Fire OS, with the same code,
    // the same Media3 and its own manifest (src/amazon). TV_PLATFORM is the platform the app names
    // in X-Maktaba-Client and the playback profile it asks for (streaming's `androidtv`, `firetv`).
    flavorDimensions += "store"
    productFlavors {
        create("google") {
            dimension = "store"
            isDefault = true
            buildConfigField("String", "TV_PLATFORM", "\"androidtv\"")
        }
        create("amazon") {
            dimension = "store"
            buildConfigField("String", "TV_PLATFORM", "\"firetv\"")
        }
    }

    // Google Play (docs/google-play.md), the `google` flavor's bundle. The release takes its version from the tag
    // (-Pmaktaba.versionName / -Pmaktaba.versionCode, tools/release/play-version.sh) and is signed
    // with the Play upload key only when the environment names it (ANDROID_UPLOAD_KEYSTORE_FILE,
    // ANDROID_UPLOAD_KEY_ALIAS, ANDROID_UPLOAD_KEY_PASSWORD, ANDROID_UPLOAD_STORE_PASSWORD);
    // without it the bundle is unsigned. No key is ever in the repository. The `amazon` APK is
    // built unsigned and signed by tv-release.yml with the Amazon upload key (docs/stores/amazon.md).
    val uploadStore = providers.environmentVariable("ANDROID_UPLOAD_KEYSTORE_FILE").orNull
    signingConfigs {
        if (!uploadStore.isNullOrBlank()) {
            create("upload") {
                val keyPass = providers.environmentVariable("ANDROID_UPLOAD_KEY_PASSWORD").get()
                storeFile = file(uploadStore)
                storePassword = providers.environmentVariable("ANDROID_UPLOAD_STORE_PASSWORD").orNull
                    ?.takeIf { it.isNotEmpty() } ?: keyPass
                keyAlias = providers.environmentVariable("ANDROID_UPLOAD_KEY_ALIAS").get()
                keyPassword = keyPass
            }
        }
    }

    productFlavors.named("google") { signingConfig = signingConfigs.findByName("upload") }

    buildTypes {
        release {
            isMinifyEnabled = true
            isShrinkResources = true
            proguardFiles(getDefaultProguardFile("proguard-android-optimize.txt"), "proguard-rules.pro")
        }
    }

    // English and Arabic: the system's per-app language setting lists both.
    androidResources {
        generateLocaleConfig = true
    }

    buildFeatures {
        compose = true
        buildConfig = true
    }

    // English and Arabic ship together: a string with no Arabic fails lint (and TranslationsTest).
    lint {
        error += "MissingTranslation"
    }

    compileOptions {
        sourceCompatibility = JavaVersion.VERSION_17
        targetCompatibility = JavaVersion.VERSION_17
    }

    // The code screen's and the tab bar's (story 51.11) tests run on the JVM (Robolectric): they need
    // the app's resources, and
    // Robolectric's native graphics for the screenshots.
    testOptions {
        unitTests.isIncludeAndroidResources = true
        unitTests.isReturnDefaultValues = true
        unitTests.all { test ->
            test.systemProperty("robolectric.graphicsMode", "NATIVE")
            test.maxHeapSize = "3g"
            // the demo library (story 60.09) the Demo*ScreenshotTests read at test time
            test.inputs.dir(file("../../../../tests/fixtures/demo-library")).withPathSensitivity(PathSensitivity.RELATIVE).withPropertyName("demoLibrary")
        }
    }
}

// Story 58.05 T01: the Fire TV build carries no Google Play code. Fire OS has no Play Services, so
// a library that needs them would crash there; the check fails the build instead. It reads the
// resolved `amazonRelease` runtime classpath (every module, transitively) and names the path that
// brought each banned module in. `-Pmaktaba.gmsFixture` adds play-services-base to the amazon
// flavor, so CI proves the check fails when one arrives.
abstract class CheckAmazonDependencies : DefaultTask() {
    @get:Input abstract val root: Property<ResolvedComponentResult>
    @get:Input abstract val banned: ListProperty<String>

    @TaskAction
    fun check() {
        val found = mutableListOf<String>()
        val seen = HashSet<Any>()
        fun walk(c: ResolvedComponentResult, path: List<String>) {
            if (!seen.add(c.id)) return
            val id = c.id
            val name = if (id is ModuleComponentIdentifier) "${id.group}:${id.module}:${id.version}" else id.displayName
            if (id is ModuleComponentIdentifier && banned.get().any { id.group == it || id.group.startsWith("$it.") }) {
                found += (path + name).joinToString(" -> ")
            }
            for (d in c.dependencies) if (d is ResolvedDependencyResult) walk(d.selected, path + name)
        }
        walk(root.get(), emptyList())
        if (found.isNotEmpty()) {
            throw GradleException(
                "The Fire TV (amazon) build must not carry Google Play code; found:\n  " + found.joinToString("\n  "),
            )
        }
        logger.lifecycle("checkAmazonDependencies: no ${banned.get().joinToString(", ")} module in amazonRelease")
    }
}

val checkAmazonDependencies = tasks.register<CheckAmazonDependencies>("checkAmazonDependencies") {
    group = "verification"
    description = "Fails when the amazonRelease runtime classpath holds Google Play Services, Firebase or Play Billing."
    root.set(configurations.named("amazonReleaseRuntimeClasspath").flatMap { it.incoming.resolutionResult.rootComponent })
    banned.set(listOf("com.google.android.gms", "com.google.firebase", "com.android.billingclient"))
}

// Story 58.05 T05: the merged amazonRelease manifest is a Fire TV app's. Fire OS reads either
// launcher category (Amazon's pages do not settle which), so both are declared; a Fire TV has no
// touchscreen, and Leanback is not required (Amazon's Appstore filters on required features).
abstract class CheckAmazonManifest : DefaultTask() {
    @get:InputFile @get:PathSensitive(PathSensitivity.NONE) abstract val manifest: RegularFileProperty

    @TaskAction
    fun check() {
        val doc = DocumentBuilderFactory.newInstance().apply { isNamespaceAware = true }
            .newDocumentBuilder().parse(manifest.get().asFile)
        val ns = "http://schemas.android.com/apk/res/android"
        val problems = mutableListOf<String>()
        fun elements(tag: String): List<Element> {
            val l = doc.getElementsByTagName(tag)
            return (0 until l.length).map { l.item(it) as Element }
        }
        val features = elements("uses-feature").associate { it.getAttributeNS(ns, "name") to it.getAttributeNS(ns, "required") }
        for (f in listOf("android.software.leanback", "android.hardware.touchscreen", "amazon.hardware.fire_tv")) {
            if (features[f] != "false") problems += "uses-feature $f must be declared with required=false (is ${features[f] ?: "missing"})"
        }
        val main = elements("activity").firstOrNull { it.getAttributeNS(ns, "name").endsWith(".MainActivity") }
        val categories = main?.getElementsByTagName("intent-filter")?.let { fl ->
            (0 until fl.length).map { fl.item(it) as Element }.filter { f ->
                val actions = f.getElementsByTagName("action")
                (0 until actions.length).any { (actions.item(it) as Element).getAttributeNS(ns, "name") == "android.intent.action.MAIN" }
            }.flatMap { f ->
                val c = f.getElementsByTagName("category")
                (0 until c.length).map { (c.item(it) as Element).getAttributeNS(ns, "name") }
            }
        }.orEmpty()
        for (c in listOf("android.intent.category.LEANBACK_LAUNCHER", "android.intent.category.LAUNCHER")) {
            if (c !in categories) problems += "MainActivity has no MAIN intent filter with $c"
        }
        val text = manifest.get().asFile.readText()
        for (g in listOf("com.google.android.gms", "com.google.firebase", "com.android.vending.BILLING")) {
            if (g in text) problems += "the manifest names $g"
        }
        if (problems.isNotEmpty()) throw GradleException("The Fire TV (amazon) manifest is wrong:\n  " + problems.joinToString("\n  "))
        logger.lifecycle("checkAmazonManifest: both launchers, touchscreen, Leanback and Fire TV not required, no Google Play")
    }
}

val checkAmazonManifest = tasks.register<CheckAmazonManifest>("checkAmazonManifest") {
    group = "verification"
    description = "Checks the merged amazonRelease manifest: both launcher filters, no required touchscreen or Leanback, no Google Play."
}
androidComponents {
    onVariants(selector().withName("amazonRelease")) { variant ->
        checkAmazonManifest.configure { manifest.set(variant.artifacts.get(SingleArtifact.MERGED_MANIFEST)) }
    }
}

tasks.named("check") { dependsOn(checkAmazonDependencies, checkAmazonManifest) }

// T04's clip (a 4 s, 64×36 H.264 file with two AAC tracks) is the phone Player's test clip, packed
// into the instrumented tests' APK rather than copied.
android.sourceSets.getByName("androidTest").assets.srcDir("../../../android/player/src/test/resources/media")

// The references live beside the tests and change only in a reviewed PR.
roborazzi {
    outputDir.set(file("src/test/screenshots"))
}

dependencies {
    implementation(project(":core"))

    // Compose for TV: focus-aware Material (cards, buttons, surfaces with focus visuals).
    implementation(tv.tv.material)

    // HLS through Media3, every /stream/ request through the core's authorizing OkHttp client.
    implementation(libs.media3.exoplayer)
    implementation(libs.media3.exoplayer.hls)
    implementation(libs.media3.datasource.okhttp)
    implementation(libs.media3.ui)

    // The sign-in QR (a matrix of the approve page's address, drawn by the app).
    implementation(tv.zxing.core)

    // T01's proof: the check above must fail with Play Services in the Fire TV build
    if (providers.gradleProperty("maktaba.gmsFixture").isPresent) {
        add("amazonImplementation", "com.google.android.gms:play-services-base:18.5.0")
    }

    testImplementation(libs.junit)
    testImplementation(libs.okhttp.mockwebserver)
    testImplementation(libs.kotlinx.coroutines.test)
    // StringsXml: the tests read the TV's and the phones' English and Arabic as Android does
    testImplementation(testFixtures(project(":client")))
    // the code screen on Robolectric, and its screenshots (Roborazzi)
    testImplementation(platform(libs.compose.bom))
    testImplementation(libs.compose.ui.test.junit4)
    testImplementation(libs.androidx.test.core)
    testImplementation(libs.androidx.test.ext.junit)
    testImplementation(libs.espresso.core)
    testImplementation(libs.robolectric)
    testImplementation(libs.roborazzi)
    testImplementation(libs.roborazzi.compose)
    testImplementation(libs.roborazzi.junit.rule)

    // The permissions walkthrough's Compose UI tests on a TV or emulator (connectedGoogleDebugAndroidTest; the Fire TV build's on Google's TV image without Play, connectedAmazonDebugAndroidTest).
    androidTestImplementation(platform(libs.compose.bom))
    androidTestImplementation(libs.compose.ui.test.junit4)
    androidTestImplementation(libs.androidx.test.runner)
    androidTestImplementation(libs.androidx.test.ext.junit)
    androidTestImplementation(libs.espresso.core)
    // T04's mock home on the emulator (story 58.05)
    androidTestImplementation(libs.okhttp.mockwebserver)
    debugImplementation(libs.compose.ui.test.manifest)
}
