# kotlinx.serialization keeps its generated serializers through its own consumer rules.
# The API models are read by name from JSON:
-keep class com.hamzalabs.maktaba.core.api.** { *; }
