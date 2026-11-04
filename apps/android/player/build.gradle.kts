plugins {
    alias(libs.plugins.android.application)
    alias(libs.plugins.kotlin.compose)
    // the shell's routes are typed (@Serializable), as Navigation Compose reads them
    alias(libs.plugins.kotlin.serialization)
    // screenshot tests on the JVM (Robolectric): recordRoborazziDebug writes the references,
    // verifyRoborazziDebug (CI) fails when a screen no longer matches them
    alias(libs.plugins.roborazzi)
}

android {
    namespace = "com.hamzalabs.maktaba.player"
    compileSdk = 37

    defaultConfig {
        applicationId = "com.hamzalabs.maktaba.player"
        minSdk = 26
        targetSdk = 37
        versionCode = providers.gradleProperty("maktaba.versionCode").orNull?.toInt() ?: 1
        versionName = providers.gradleProperty("maktaba.versionName").orNull ?: "0.1.0"
        // story 51.11's build switch, read by capabilitiesGate(): on unless -Pmaktaba.capabilitiesGate=off
        val capabilitiesGate = providers.gradleProperty("maktaba.capabilitiesGate").orNull.orEmpty()
        require(capabilitiesGate.all { it.isLetterOrDigit() }) { "maktaba.capabilitiesGate is on or off" }
        buildConfigField("String", "CAPABILITIES_GATE", "\"$capabilitiesGate\"")
        // One app for phones, foldables and tablets (one Google Play listing): the layout follows
        // the window, and the device the API is told comes from the screen (ClientHeader.deviceFor).
        // Its name is res/values*/strings.xml's app_name ("Maktaba Player", «مشغل مكتبة»).
    }

    // Google Play (docs/google-play.md). The release takes its version from the tag
    // (-Pmaktaba.versionName / -Pmaktaba.versionCode, tools/release/play-version.sh) and is signed
    // with the Play upload key only when the environment names it (ANDROID_UPLOAD_KEYSTORE_FILE,
    // ANDROID_UPLOAD_KEY_ALIAS, ANDROID_UPLOAD_KEY_PASSWORD, ANDROID_UPLOAD_STORE_PASSWORD);
    // without it the bundle is unsigned. No key is ever in the repository.
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

    buildTypes {
        release {
            signingConfig = signingConfigs.findByName("upload")
            isMinifyEnabled = true
            isShrinkResources = true
            proguardFiles(getDefaultProguardFile("proguard-android-optimize.txt"), "proguard-rules.pro")
        }
    }

    // English and Arabic: Android's per-app language setting lists both.
    androidResources {
        generateLocaleConfig = true
    }
    // Before Android 13 the language is chosen in the app (48.31): a store install keeps every
    // language's resources, so Arabic is there when chosen on an English phone
    bundle {
        language {
            enableSplit = false
        }
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

    // The Player's UI tests run on the JVM (Robolectric), against a fake home (MockWebServer):
    // they need the app's resources, and Robolectric's native graphics for the screenshots.
    testOptions {
        unitTests.isIncludeAndroidResources = true
        unitTests.isReturnDefaultValues = true
        unitTests.all { test ->
            test.systemProperty("robolectric.graphicsMode", "NATIVE")
            test.maxHeapSize = "3g"
        }
    }
}

// The references live beside the tests and change only in a reviewed PR.
roborazzi {
    outputDir.set(file("src/test/screenshots"))
}

dependencies {
    implementation(project(":core"))
    implementation(libs.media3.exoplayer)
    implementation(libs.media3.exoplayer.hls)
    implementation(libs.media3.datasource.okhttp)
    implementation(libs.media3.ui)

    // Epic 48: the shell (adaptive navigation, typed routes), and what the screen and player
    // stories build on: images (Coil), the media session and Cast (48.33)
    implementation(libs.adaptive.navigation.suite)
    implementation(libs.navigation.compose)
    implementation(libs.kotlinx.serialization.json)
    implementation(libs.coil.compose)
    implementation(libs.coil.network.okhttp)
    implementation(libs.media3.session)
    implementation(libs.media3.cast)
    implementation(libs.play.services.cast.framework)

    // JVM tests: unit tests, Compose UI tests on Robolectric against a fake home, Roborazzi
    testImplementation(testFixtures(project(":client")))
    testImplementation(libs.junit)
    testImplementation(libs.kotlinx.coroutines.test)
    testImplementation(libs.okhttp.mockwebserver)
    // the player plays the test streams for real (decoders faked on the JVM): sound selected, tracks, resume
    testImplementation(libs.media3.test.utils)
    testImplementation(libs.media3.test.utils.robolectric)
    testImplementation(platform(libs.compose.bom))
    testImplementation(libs.compose.ui.test.junit4)
    testImplementation(libs.androidx.test.core)
    testImplementation(libs.androidx.test.ext.junit)
    testImplementation(libs.espresso.core)
    testImplementation(libs.robolectric)
    testImplementation(libs.roborazzi)
    testImplementation(libs.roborazzi.compose)
    testImplementation(libs.roborazzi.junit.rule)
    debugImplementation(libs.compose.ui.test.manifest)
}
