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
        versionCode = providers.gradleProperty("maktaba.versionCode").orNull?.toInt() ?: 1
        versionName = providers.gradleProperty("maktaba.versionName").orNull ?: "0.1.0"
        // One app for phones, foldables and tablets (one Google Play listing): the layout follows
        // the window, and the device the API is told comes from the screen (ClientHeader.deviceFor).
        // Its name is res/values*/strings.xml's app_name ("Maktaba Remote", «ريموت مكتبة»).
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
