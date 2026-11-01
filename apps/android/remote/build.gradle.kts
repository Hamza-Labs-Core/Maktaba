plugins {
    alias(libs.plugins.android.application)
    alias(libs.plugins.kotlin.compose)
}

android {
    namespace = "com.hamzalabs.maktaba.remote"
    compileSdk = 37

    defaultConfig {
        applicationId = "com.hamzalabs.maktaba.remote"
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
        }
        create("foldable") {
            dimension = "form"
            applicationIdSuffix = ".foldable"
        }
        create("tablet") {
            dimension = "form"
            applicationIdSuffix = ".tablet"
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
    implementation(libs.adaptive.navigation.suite)

    testImplementation(libs.junit)
    // StringsXml: the tests read the app's English and Arabic as Android does
    testImplementation(testFixtures(project(":client")))
}
