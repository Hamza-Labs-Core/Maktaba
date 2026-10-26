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
            resValue("string", "app_name", "Maktaba Remote")
        }
        create("foldable") {
            dimension = "form"
            applicationIdSuffix = ".foldable"
            resValue("string", "app_name", "Maktaba Remote")
        }
        create("tablet") {
            dimension = "form"
            applicationIdSuffix = ".tablet"
            resValue("string", "app_name", "Maktaba Remote")
        }
    }

    buildTypes {
        release {
            isMinifyEnabled = true
            isShrinkResources = true
            proguardFiles(getDefaultProguardFile("proguard-android-optimize.txt"), "proguard-rules.pro")
        }
    }

    buildFeatures {
        compose = true
        resValues = true
    }

    compileOptions {
        sourceCompatibility = JavaVersion.VERSION_17
        targetCompatibility = JavaVersion.VERSION_17
    }
}

dependencies {
    implementation(project(":core"))
    implementation(libs.adaptive.navigation.suite)
}
