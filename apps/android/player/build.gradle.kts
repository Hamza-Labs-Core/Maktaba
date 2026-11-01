plugins {
    alias(libs.plugins.android.application)
    alias(libs.plugins.kotlin.compose)
    // the shell's routes are typed (@Serializable), as Navigation Compose reads them
    alias(libs.plugins.kotlin.serialization)
    // screenshot tests on the JVM (Robolectric): recordRoborazziPhoneDebug writes the references,
    // verifyRoborazziPhoneDebug (CI) fails when a screen no longer matches them
    alias(libs.plugins.roborazzi)
}

android {
    namespace = "com.hamzalabs.maktaba.player"
    compileSdk = 37

    defaultConfig {
        applicationId = "com.hamzalabs.maktaba.player"
        minSdk = 26
        targetSdk = 37
        versionCode = 1
        versionName = "0.1.0"
    }

    // One code base, three store listings (Epic 42): the layout follows the window on every
    // one of them; the flavour is the listing's identity and name.
    flavorDimensions += "form"
    productFlavors {
        create("phone") {
            dimension = "form"
            resValue("string", "app_name", "Maktaba Player")
        }
        create("foldable") {
            dimension = "form"
            applicationIdSuffix = ".foldable"
            resValue("string", "app_name", "Maktaba Player")
        }
        create("tablet") {
            dimension = "form"
            applicationIdSuffix = ".tablet"
            resValue("string", "app_name", "Maktaba Player")
        }
    }

    buildTypes {
        release {
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
        resValues = true
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
