package com.hamzalabs.maktaba.desktop

import com.hamzalabs.maktaba.core.api.AuthTokens
import org.junit.Assert.assertArrayEquals
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertNull
import org.junit.Assert.assertTrue
import org.junit.Rule
import org.junit.Test
import org.junit.rules.TemporaryFolder
import java.io.File
import java.nio.file.Files
import java.nio.file.attribute.PosixFilePermission

class StorageTest {
    @get:Rule val dir = TemporaryFolder()

    @Test
    fun keepsStringsAcrossRestarts() {
        val f = File(dir.root, "player.json")
        FileStore(f).apply {
            put("lang", "ar")
            put("gone", "x")
            put("gone", null)
        }
        val again = FileStore(f)
        assertEquals("ar", again.get("lang"))
        assertNull(again.get("gone"))
        assertFalse(File(dir.root, "player.json.tmp").exists())
    }

    @Test
    fun aBrokenFileReadsAsEmpty() {
        val f = File(dir.root, "player.json").apply { writeText("{not json") }
        assertNull(FileStore(f).get("lang"))
    }

    @Test
    fun tokensAreSealedPerHomeAndOnlyForThisUser() {
        val stores = Stores(dir.root)
        val a = stores.tokens("http://a:8088")
        a.save(AuthTokens("access-a", "refresh-a"))
        stores.tokens("http://b:8088").save(AuthTokens("access-b", "refresh-b"))
        assertEquals("access-a", Stores(dir.root).tokens("http://a:8088").access)
        assertEquals("refresh-b", Stores(dir.root).tokens("http://b:8088").refresh)
        val onDisk = File(dir.root, "tokens.json").readText()
        assertFalse("tokens stored in the clear", onDisk.contains("access-a") || onDisk.contains("refresh-b"))
        if (!AppDirs.isWindows) {
            for (name in listOf("tokens.json", "tokens.key")) {
                val perms = Files.getPosixFilePermissions(File(dir.root, name).toPath())
                assertEquals(name, setOf(PosixFilePermission.OWNER_READ, PosixFilePermission.OWNER_WRITE), perms)
            }
        }
        a.clear()
        assertNull(Stores(dir.root).tokens("http://a:8088").access)
        assertEquals("access-b", Stores(dir.root).tokens("http://b:8088").access)
    }

    @Test
    fun aLostKeyIsASignOutNotACrash() {
        Stores(dir.root).tokens("http://a:8088").save(AuthTokens("access-a", "refresh-a"))
        File(dir.root, "tokens.key").delete()
        if (!AppDirs.isWindows) assertNull(Stores(dir.root).tokens("http://a:8088").access)
    }

    @Test
    fun theKeyFileBoxOpensWhatItSealed() {
        val box = KeyFileSecretBox(File(dir.root, "k"))
        val sealed = box.seal("hello".toByteArray())
        assertTrue(sealed.size > 12 + 5)
        assertArrayEquals("hello".toByteArray(), KeyFileSecretBox(File(dir.root, "k")).open(sealed))
    }
}
