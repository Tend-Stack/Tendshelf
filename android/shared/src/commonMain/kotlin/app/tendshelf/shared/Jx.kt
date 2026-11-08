package app.tendshelf.shared

import kotlinx.serialization.json.Json
import kotlinx.serialization.json.JsonArray
import kotlinx.serialization.json.JsonElement
import kotlinx.serialization.json.JsonNull
import kotlinx.serialization.json.JsonObject
import kotlinx.serialization.json.JsonPrimitive
import kotlinx.serialization.json.booleanOrNull
import kotlinx.serialization.json.doubleOrNull
import kotlinx.serialization.json.longOrNull

/*
 * Reading JSON the way the app always did (Android's org.json), on top of kotlinx.serialization's tree. The app moved
 * from org.json to this so the parsers can live in commonMain for a later iOS app; what the parsers accept, their
 * defaults and their refusals must not change, so these helpers copy org.json's rules:
 *  - get* throws on a missing key and on a value that cannot be coerced; opt* gives its fallback instead;
 *  - a number or a string is coerced the way org.json did ("12" reads as 12, 12 reads as "12", "true" as true);
 *  - a JSON null read as text is the text "null" (org.json's quirk), for getString and optString alike.
 * Unknown keys are ignored by construction (nothing is bound to a class).
 */

/** Thrown for every refusal, as org.json's JSONException was; callers catch Exception. */
class JsonShapeException(message: String) : Exception(message)

// Lenient like org.json's tokener: unquoted words were accepted before.
private val reader = Json { isLenient = true }

/** Parses text that must be a JSON object. */
fun parseJsonObject(text: String): JsonObject {
    val e = try {
        reader.parseToJsonElement(text)
    } catch (_: Exception) {
        throw JsonShapeException("not JSON")
    }
    return e as? JsonObject ?: throw JsonShapeException("not an object")
}

fun JsonObject.has(name: String): Boolean = containsKey(name)

/** True when the key is missing or holds JSON null (org.json's isNull). */
fun JsonObject.isNull(name: String): Boolean = this[name].let { it == null || it is JsonNull }

private fun JsonElement.coerceString(): String = when (this) {
    is JsonNull -> "null"
    is JsonPrimitive -> content
    else -> toString()
}

fun JsonObject.optString(name: String, fallback: String = ""): String = this[name]?.coerceString() ?: fallback

fun JsonObject.getString(name: String): String = this[name]?.coerceString() ?: throw JsonShapeException("no value $name")

private fun JsonElement?.asDouble(): Double? = (this as? JsonPrimitive)?.let { p ->
    if (p.isString) p.content.trim().toDoubleOrNull() else p.doubleOrNull
}

private fun JsonElement?.asLong(): Long? = (this as? JsonPrimitive)?.let { p ->
    if (p.isString) p.content.trim().let { s -> s.toLongOrNull() ?: s.toDoubleOrNull()?.toLong() } else p.longOrNull ?: p.doubleOrNull?.toLong()
}

private fun JsonElement?.asBoolean(): Boolean? = (this as? JsonPrimitive)?.let { p ->
    if (p.isString) {
        when (p.content.lowercase()) {
            "true" -> true
            "false" -> false
            else -> null
        }
    } else {
        p.booleanOrNull
    }
}

fun JsonObject.getLong(name: String): Long = this[name].asLong() ?: throw JsonShapeException("no long $name")
fun JsonObject.getInt(name: String): Int = getLong(name).toInt()
fun JsonObject.getDouble(name: String): Double = this[name].asDouble() ?: throw JsonShapeException("no double $name")
fun JsonObject.getBoolean(name: String): Boolean = this[name].asBoolean() ?: throw JsonShapeException("no boolean $name")

fun JsonObject.optLong(name: String, fallback: Long = 0): Long = this[name].asLong() ?: fallback
fun JsonObject.optInt(name: String, fallback: Int = 0): Int = this[name].asLong()?.toInt() ?: fallback
fun JsonObject.optDouble(name: String, fallback: Double = Double.NaN): Double = this[name].asDouble() ?: fallback
fun JsonObject.optBoolean(name: String, fallback: Boolean = false): Boolean = this[name].asBoolean() ?: fallback

fun JsonObject.optObject(name: String): JsonObject? = this[name] as? JsonObject
fun JsonObject.optArray(name: String): JsonArray? = this[name] as? JsonArray
fun JsonObject.getObject(name: String): JsonObject = optObject(name) ?: throw JsonShapeException("no object $name")
fun JsonObject.getArray(name: String): JsonArray = optArray(name) ?: throw JsonShapeException("no array $name")

fun JsonArray.optObject(i: Int): JsonObject? = getOrNull(i) as? JsonObject
fun JsonArray.getObject(i: Int): JsonObject = optObject(i) ?: throw JsonShapeException("no object at $i")
fun JsonArray.getString(i: Int): String = getOrNull(i)?.coerceString() ?: throw JsonShapeException("no value at $i")
