plugins {
    alias(libs.plugins.android.application)
    alias(libs.plugins.kotlin.compose)
    alias(libs.plugins.kotlin.serialization)
    // screenshot tests on the JVM (Robolectric): recordRoborazziDebug writes the references,
    // verifyRoborazziDebug (CI) fails when a screen no longer matches them
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
        testInstrumentationRunner = "androidx.test.runner.AndroidJUnitRunner"
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

    // English and Arabic: the system's per-app language setting lists both.
    androidResources {
        generateLocaleConfig = true
    }

    buildFeatures {
        compose = true
    }

    // English and Arabic ship together: a string with no Arabic fails lint (and TranslationsTest).
    lint {
        error += "MissingTranslation"
    }

    compileOptions {
        sourceCompatibility = JavaVersion.VERSION_17
        targetCompatibility = JavaVersion.VERSION_17
    }

    // The code screen's tests run on the JVM (Robolectric): they need the app's resources, and
    // Robolectric's native graphics for the screenshots.
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

    // Compose for TV: focus-aware Material (cards, buttons, surfaces with focus visuals).
    implementation(tv.tv.material)

    // HLS through Media3, every /stream/ request through the core's authorizing OkHttp client.
    implementation(libs.media3.exoplayer)
    implementation(libs.media3.exoplayer.hls)
    implementation(libs.media3.datasource.okhttp)
    implementation(libs.media3.ui)

    // The sign-in QR (a matrix of the approve page's address, drawn by the app).
    implementation(tv.zxing.core)

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
    testImplementation(libs.robolectric)
    testImplementation(libs.roborazzi)
    testImplementation(libs.roborazzi.compose)
    testImplementation(libs.roborazzi.junit.rule)

    // The permissions walkthrough's Compose UI tests on a TV or emulator (connectedDebugAndroidTest).
    androidTestImplementation(platform(libs.compose.bom))
    androidTestImplementation(libs.compose.ui.test.junit4)
    androidTestImplementation(libs.androidx.test.runner)
    androidTestImplementation(libs.androidx.test.ext.junit)
    androidTestImplementation(libs.espresso.core)
    debugImplementation(libs.compose.ui.test.manifest)
}
