package app.tendshelf.shared

import kotlin.annotation.AnnotationRetention.SOURCE
import kotlin.annotation.AnnotationTarget.FUNCTION

/**
 * `@Synchronized` for common code: a lock on the instance on the JVM (Android), nothing on iOS, where the app calls
 * the same object from one serial queue. Kotlin has no common lock, and kotlin.jvm.Synchronized may not be used from
 * commonMain, so this optional expectation is actualised by a typealias in jvmMain.
 */
@OptIn(ExperimentalMultiplatform::class)
@OptionalExpectation
@Target(FUNCTION)
@Retention(SOURCE)
internal expect annotation class SynchronizedOnJvm()
