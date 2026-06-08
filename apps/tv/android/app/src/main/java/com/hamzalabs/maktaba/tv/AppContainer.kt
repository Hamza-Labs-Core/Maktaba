package com.hamzalabs.maktaba.tv

import android.content.Context
import com.hamzalabs.maktaba.tv.data.SettingsStore
import com.hamzalabs.maktaba.tv.data.api.TokenStore
import com.hamzalabs.maktaba.tv.data.repository.MediaRepository

/**
 * Hand-wired dependency graph. A production app would use Hilt; the
 * scaffold keeps a single container created by [MaktabaTVApp] and read
 * via `(LocalContext.current.applicationContext as MaktabaTVApp).container`.
 */
class AppContainer(context: Context) {
    val settings = SettingsStore(context)
    val tokens = TokenStore(context)
    val repository = MediaRepository(settings, tokens)
}
