package com.hamzalabs.maktaba.core.api

import kotlinx.serialization.KSerializer
import kotlinx.serialization.builtins.ListSerializer
import kotlinx.serialization.builtins.MapSerializer
import kotlinx.serialization.builtins.nullable
import kotlinx.serialization.builtins.serializer
import kotlinx.serialization.descriptors.SerialDescriptor
import kotlinx.serialization.encoding.Decoder
import kotlinx.serialization.encoding.Encoder
import kotlinx.serialization.json.JsonArray
import kotlinx.serialization.json.JsonDecoder
import kotlinx.serialization.json.JsonElement
import kotlinx.serialization.json.JsonObject
import kotlinx.serialization.json.JsonPrimitive
import kotlinx.serialization.json.intOrNull

// Lenient decoders for the Overview's optional fields (53.12): one malformed value is skipped and its
// field falls back to its default (no name: the node id), as web and the Apple Remote do, instead of
// failing the whole answer. Each reads the field as a JsonElement and keeps only well-formed parts.
// Required fields keep their strict serializers.

private fun Decoder.element(): JsonElement? = (this as? JsonDecoder)?.decodeJsonElement()

private fun JsonElement.text(): String? = (this as? JsonPrimitive)?.takeIf { it.isString }?.content

/** A `{id: name}` map: entries whose value is not text are left out; anything but an object is no names. */
internal object LenientNames : KSerializer<Map<String, String>> {
    private val strict = MapSerializer(String.serializer(), String.serializer())
    override val descriptor: SerialDescriptor = strict.descriptor
    override fun serialize(encoder: Encoder, value: Map<String, String>) = strict.serialize(encoder, value)
    override fun deserialize(decoder: Decoder): Map<String, String> {
        val e = decoder.element() ?: return strict.deserialize(decoder)
        return (e as? JsonObject)?.mapNotNull { (k, v) -> v.text()?.let { k to it } }?.toMap().orEmpty()
    }
}

/** A list of text: entries that are not text are left out; anything but an array is empty. */
internal object LenientStrings : KSerializer<List<String>> {
    private val strict = ListSerializer(String.serializer())
    override val descriptor: SerialDescriptor = strict.descriptor
    override fun serialize(encoder: Encoder, value: List<String>) = strict.serialize(encoder, value)
    override fun deserialize(decoder: Decoder): List<String> {
        val e = decoder.element() ?: return strict.deserialize(decoder)
        return (e as? JsonArray)?.mapNotNull { it.text() }.orEmpty()
    }
}

/** A count: anything but a whole JSON number in Int range is 0. */
internal object LenientCount : KSerializer<Int> {
    override val descriptor: SerialDescriptor = Int.serializer().descriptor
    override fun serialize(encoder: Encoder, value: Int) = encoder.encodeInt(value)
    override fun deserialize(decoder: Decoder): Int {
        val e = decoder.element() ?: return decoder.decodeInt()
        return (e as? JsonPrimitive)?.takeIf { !it.isString }?.intOrNull ?: 0
    }
}

/** The Overview's machines: anything but an object is none (as from a home before them). */
internal object LenientMachines : KSerializer<BoardMachines?> {
    private val strict = BoardMachines.serializer().nullable
    override val descriptor: SerialDescriptor = strict.descriptor
    override fun serialize(encoder: Encoder, value: BoardMachines?) = strict.serialize(encoder, value)
    override fun deserialize(decoder: Decoder): BoardMachines? {
        val d = decoder as? JsonDecoder ?: return strict.deserialize(decoder)
        val e = d.decodeJsonElement() as? JsonObject ?: return null
        return d.json.decodeFromJsonElement(BoardMachines.serializer(), e)
    }
}
