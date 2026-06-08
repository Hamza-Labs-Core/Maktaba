package com.hamzalabs.maktaba.tv.data.api

import com.hamzalabs.maktaba.tv.data.models.AuthTokens
import com.hamzalabs.maktaba.tv.data.models.LibraryList
import com.hamzalabs.maktaba.tv.data.models.LoginRequest
import com.hamzalabs.maktaba.tv.data.models.Recommendations
import com.hamzalabs.maktaba.tv.data.models.RefreshRequest
import com.hamzalabs.maktaba.tv.data.models.SearchResponse
import com.hamzalabs.maktaba.tv.data.models.User
import retrofit2.http.Body
import retrofit2.http.GET
import retrofit2.http.POST
import retrofit2.http.Path
import retrofit2.http.Query

/**
 * Retrofit description of the Maktaba REST API — the same server the
 * web and mobile clients use. Routes are defined in `api/internal/handlers`.
 */
interface MaktabaApi {

    @POST("api/auth/login")
    suspend fun login(@Body body: LoginRequest): AuthTokens

    @POST("api/auth/refresh")
    suspend fun refresh(@Body body: RefreshRequest): AuthTokens

    @GET("api/auth/me")
    suspend fun me(): User

    @GET("api/libraries")
    suspend fun libraries(): LibraryList

    @GET("api/libraries/{id}/items")
    suspend fun libraryItems(@Path("id") libraryId: String): SearchResponse

    @GET("api/recommendations")
    suspend fun recommendations(): Recommendations

    @GET("api/search")
    suspend fun search(@Query("q") query: String): SearchResponse
}
