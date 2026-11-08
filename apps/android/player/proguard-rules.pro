# kotlinx.serialization keeps its generated serializers through its own consumer rules.
# The API models are read by name from JSON:
-keep class com.hamzalabs.maktaba.core.api.** { *; }

# Crash frames keep their lines (Epic 53 story 53.16): the release's mapping.txt reads an
# obfuscated frame back to its class, method and line only by the line it names. The source
# file's name is replaced, so no file name leaves the device.
-keepattributes SourceFile,LineNumberTable
-renamesourcefileattribute SourceFile
