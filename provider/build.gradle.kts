plugins { kotlin("jvm") version "2.2.21"; application }
repositories { mavenCentral() }
dependencies {
    implementation("com.squareup.okhttp3:okhttp:4.12.0")
    implementation("org.jetbrains.kotlinx:kotlinx-coroutines-core:1.9.0")
    implementation("org.json:json:20240303")
    testImplementation(kotlin("test"))
    testImplementation("com.squareup.okhttp3:mockwebserver:4.12.0")
}
kotlin { jvmToolchain(17) }
application { mainClass.set("com.yunx.server.MainKt") }
tasks.jar { archiveClassifier.set("thin") }
tasks.register<Jar>("fatJar") {
    archiveFileName.set("yunx-provider.jar")
    duplicatesStrategy = DuplicatesStrategy.EXCLUDE
    manifest { attributes["Main-Class"] = "com.yunx.server.MainKt" }
    from(sourceSets.main.get().output)
    dependsOn(configurations.runtimeClasspath)
    from({ configurations.runtimeClasspath.get().map { if (it.isDirectory) it else zipTree(it) } })
    exclude("META-INF/*.SF", "META-INF/*.RSA", "META-INF/*.DSA")
    isPreserveFileTimestamps = false
    isReproducibleFileOrder = true
}

tasks.test { environment("YUNX_PROVIDER_DEVICE_ID", "0123456789abcdef0123456789abcdef") }
