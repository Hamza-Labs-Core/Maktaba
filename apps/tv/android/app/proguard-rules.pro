# ProGuard / R8 rules for the release build.
#
# kotlinx.serialization generates serializers reflectively-by-name at
# the call site but the @Serializable companions must survive shrinking.
-keepclassmembers class **$$serializer { *; }
-keepclasseswithmembers class com.hamzalabs.maktaba.tv.data.models.** {
    *** Companion;
}
-keep @kotlinx.serialization.Serializable class com.hamzalabs.maktaba.tv.data.models.** { *; }

# Retrofit keeps generic signatures and annotations for its proxies.
-keepattributes Signature, RuntimeVisibleAnnotations, AnnotationDefault
