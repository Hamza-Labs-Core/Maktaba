plugins {
    alias(libs.plugins.android.library)
    alias(libs.plugins.kotlin.compose)
    alias(libs.plugins.kotlin.serialization)
}

android {
    namespace = "com.hamzalabs.maktaba.core"
    compileSdk = 37

    defaultConfig {
        minSdk = 26
        testInstrumentationRunner = "androidx.test.runner.AndroidJUnitRunner"
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
        // TranslationsTest reads every Android module's strings (and the TV app's): it runs again
        // whenever one of them changes, not only the core's; LaunchThemeTest reads the launch theme
        unitTests.all { test ->
            test.inputs.files(
                fileTree(projectDir.parentFile) { include("*/src/main/res/values*/strings.xml", "core/src/main/res/values*/themes.xml", "core/src/main/res/drawable/maktaba_window.xml") },
                fileTree(projectDir.resolve("../../tv/android/app/src/main/res")) { include("values*/strings.xml") },
            ).withPropertyName("strings").withPathSensitivity(PathSensitivity.RELATIVE)
        }
    }
}

dependencies {
    api(libs.core.ktx)
    api(libs.activity.compose)
    api(libs.lifecycle.runtime.compose)
    api(platform(libs.compose.bom))
    api(libs.compose.ui)
    api(libs.compose.foundation)
    api(libs.compose.material3)
    api(libs.compose.material.icons)
    api(libs.compose.ui.tooling.preview)
    debugApi(libs.compose.ui.tooling)
    api(libs.adaptive)
    api(libs.window)

    // scanning a home's QR code (CameraX, ZXing's decoder: no Google Play services needed) and
    // drawing the sign-in code's QR
    implementation(libs.camerax.core)
    implementation(libs.camerax.camera2)
    implementation(libs.camerax.lifecycle)
    implementation(libs.camerax.view)
    implementation(libs.zxing.core)

    // the API client, sign-in and the home picker's rules, shared with the desktop player
    api(project(":client"))
    api(libs.kotlinx.coroutines.android)

    testImplementation(testFixtures(project(":client")))
    testImplementation(libs.junit)
    testImplementation(libs.okhttp.mockwebserver)
    testImplementation(libs.kotlinx.coroutines.test)

    // Compose UI tests on a device or emulator (connectedDebugAndroidTest)
    androidTestImplementation(platform(libs.compose.bom))
    androidTestImplementation(libs.compose.ui.test.junit4)
    androidTestImplementation(libs.androidx.test.runner)
    androidTestImplementation(libs.androidx.test.ext.junit)
    // Compose's test rule idles through Espresso; Android 17 needs a current one
    androidTestImplementation(libs.espresso.core)
    debugImplementation(libs.compose.ui.test.manifest)
}
