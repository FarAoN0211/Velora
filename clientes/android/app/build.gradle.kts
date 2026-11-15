import java.util.Properties

plugins {
    id("com.android.application")
}

val version = Properties().apply { rootProject.file("version.properties").inputStream().use { load(it) } }
// La clave de firma nunca entra en el repositorio. Sin ella no se puede publicar ninguna actualización.
val signingFile = File(System.getProperty("user.home"), ".config/velora-android/firma.properties")
val signing = Properties().apply { if (signingFile.exists()) signingFile.inputStream().use { load(it) } }

android {
    namespace = "es.velora.cliente"
    compileSdk = 35

    defaultConfig {
        applicationId = "es.velora.cliente"
        minSdk = 23
        targetSdk = 35
        versionCode = version.getProperty("versionCode").toInt()
        versionName = version.getProperty("versionName")
    }

    signingConfigs {
        if (signingFile.exists()) {
            create("velora") {
                storeFile = File(signingFile.parentFile, signing.getProperty("archivo"))
                storePassword = signing.getProperty("clave")
                keyAlias = signing.getProperty("alias")
                keyPassword = signing.getProperty("clave")
            }
        }
    }

    buildTypes {
        release {
            isMinifyEnabled = false
            if (signingFile.exists()) signingConfig = signingConfigs.getByName("velora")
        }
    }

    compileOptions {
        sourceCompatibility = JavaVersion.VERSION_11
        targetCompatibility = JavaVersion.VERSION_11
    }

    testOptions {
        unitTests.isIncludeAndroidResources = true
    }
}

dependencies {
    implementation("com.journeyapps:zxing-android-embedded:4.3.0") { isTransitive = false }
    implementation("com.google.zxing:core:3.3.0")
    implementation("androidx.appcompat:appcompat:1.7.0")
    // Reproductor propio de TV. Sin extensiones: decodifica el hardware del aparato.
    implementation("androidx.media3:media3-exoplayer:1.5.1")
    implementation("androidx.media3:media3-ui:1.5.1")
    testImplementation("junit:junit:4.13.2")
    // Las pruebas corren sin Android: org.json lo da el sistema en el aparato, no en el ordenador.
    testImplementation("org.json:json:20240303")
    // Las pantallas de TV se recorren en el ordenador con el mando simulado, sin aparato ni emulador.
    testImplementation("org.robolectric:robolectric:4.14.1")
}

// Las pruebas JVM usan HttpServer del JDK anfitrión. La imagen mínima de módulos de Android
// solo incluye java.base; no sirve para estas pruebas, que nunca entran en el APK.
tasks.withType<JavaCompile>().configureEach {
    if (name.contains("UnitTest")) {
        doFirst {
            val arguments = options.allCompilerArgs.toMutableList()
            val system = arguments.indexOf("--system")
            if (system >= 0) arguments[system + 1] = System.getProperty("java.home")
            options.compilerArgumentProviders.clear()
            options.compilerArgs.clear()
            options.compilerArgs.addAll(arguments)
        }
    }
}
