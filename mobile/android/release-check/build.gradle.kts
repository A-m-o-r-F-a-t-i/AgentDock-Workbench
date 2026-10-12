plugins { id("com.android.test") }

android {
    namespace = "dev.agentdock.releasecheck"
    compileSdk = 37
    defaultConfig {
        minSdk = 26
        targetSdk = 37
        testInstrumentationRunner = "androidx.test.runner.AndroidJUnitRunner"
    }
    targetProjectPath = ":app"
    // Keep the test runner in its own process/APK. Release R8 output must not
    // depend on classes retained only to satisfy an in-process test runner.
    experimentalProperties["android.experimental.self-instrumenting"] = true
    buildTypes {
        create("release") {
            isDebuggable = true
            signingConfig = signingConfigs.getByName("debug")
        }
    }
    compileOptions {
        sourceCompatibility = JavaVersion.VERSION_17
        targetCompatibility = JavaVersion.VERSION_17
    }
}
androidComponents { beforeVariants(selector().withBuildType("debug")) { it.enable = false } }

dependencies {
    implementation("androidx.test:runner:1.7.0")
    implementation("androidx.test.ext:junit:1.3.0")
    implementation("androidx.test.uiautomator:uiautomator:2.3.0")
}
