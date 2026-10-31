plugins {
    alias(libs.plugins.android.application)
    alias(libs.plugins.kotlin.compose)
    alias(libs.plugins.kotlin.serialization)
}

android {
    namespace = "com.hamzalabs.maktaba.tv"
    compileSdk = 37

    defaultConfig {
        applicationId = "com.hamzalabs.maktaba.tv"
        // Android 9: the practical floor for current Android TV / Google TV devices.
        minSdk = 28
        targetSdk = 37
        versionCode = 2
        versionName = "0.2.0"
        testInstrumentationRunner = "androidx.test.runner.AndroidJUnitRunner"
    }

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
    }

    // English and Arabic ship together: a string with no Arabic fails lint (and TranslationsTest).
    lint {
        error += "MissingTranslation"
    }

    compileOptions {
        sourceCompatibility = JavaVersion.VERSION_17
        targetCompatibility = JavaVersion.VERSION_17
    }

    testOptions {
        unitTests.isReturnDefaultValues = true
    }
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

    // The permissions walkthrough's Compose UI tests on a TV or emulator (connectedDebugAndroidTest).
    androidTestImplementation(platform(libs.compose.bom))
    androidTestImplementation(libs.compose.ui.test.junit4)
    androidTestImplementation(libs.androidx.test.runner)
    androidTestImplementation(libs.androidx.test.ext.junit)
    androidTestImplementation(libs.espresso.core)
    debugImplementation(libs.compose.ui.test.manifest)
}
