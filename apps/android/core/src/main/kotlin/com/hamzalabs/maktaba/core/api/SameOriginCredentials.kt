package com.hamzalabs.maktaba.core.api

import okhttp3.HttpUrl
import okhttp3.Interceptor
import okhttp3.Response

/**
 * A home's token goes only to the origin it was sent to: a redirect that leaves that origin
 * (scheme, host and port) is followed without `Authorization` or cookies. A network
 * interceptor sees each hop; the call's own request is where it started. (OkHttp already
 * drops `Authorization` on a redirect to another host; this keeps the rule whatever OkHttp
 * does, and for a change of scheme or port too.)
 */
object SameOriginCredentials : Interceptor {
    override fun intercept(chain: Interceptor.Chain): Response {
        val request = chain.request()
        val origin = chain.call().request().url
        if (sameOrigin(origin, request.url)) return chain.proceed(request)
        return chain.proceed(request.newBuilder().removeHeader("Authorization").removeHeader("Cookie").build())
    }

    fun sameOrigin(a: HttpUrl, b: HttpUrl): Boolean = a.scheme == b.scheme && a.host == b.host && a.port == b.port
}
