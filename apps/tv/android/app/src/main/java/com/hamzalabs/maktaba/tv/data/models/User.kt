package com.hamzalabs.maktaba.tv.data.models

import kotlinx.serialization.SerialName
import kotlinx.serialization.Serializable

/** Authenticated principal. Mirrors `userResponse` in the API. */
@Serializable
data class User(
    val id: String,
    val username: String,
    @SerialName("is_admin") val isAdmin: Boolean = false,
)

/** Body for `POST /api/auth/login`. */
@Serializable
data class LoginRequest(
    val username: String,
    val password: String,
)

/** Body for `POST /api/auth/refresh`. */
@Serializable
data class RefreshRequest(
    @SerialName("refresh_token") val refreshToken: String,
)

/** Token bundle returned by login/refresh (the native-client shape). */
@Serializable
data class AuthTokens(
    @SerialName("access_token") val accessToken: String,
    @SerialName("access_expires_in") val accessExpiresIn: Int = 0,
    @SerialName("refresh_token") val refreshToken: String,
    @SerialName("refresh_expires_in") val refreshExpiresIn: Int = 0,
    val user: User? = null,
)
