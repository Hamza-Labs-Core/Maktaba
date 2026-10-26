# R8 rules for the release build. kotlinx.serialization keeps its generated serializers through
# its own consumer rules; the API models (the core's and the TV's) are read by name from JSON.
-keep class com.hamzalabs.maktaba.core.api.** { *; }
-keep class com.hamzalabs.maktaba.tv.data.** { *; }
