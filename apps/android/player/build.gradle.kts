plugins {
    alias(libs.plugins.android.application)
    alias(libs.plugins.kotlin.compose)
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
}

dependencies {
    implementation(project(":core"))
    implementation(libs.media3.exoplayer)
    implementation(libs.media3.exoplayer.hls)
    implementation(libs.media3.datasource.okhttp)
    implementation(libs.media3.ui)
}
