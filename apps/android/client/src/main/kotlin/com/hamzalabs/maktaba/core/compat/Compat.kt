package com.hamzalabs.maktaba.core.compat

/**
 * The compatibility contract (Epic 58 story 58.01, `docs/api/levels.md`): what an app does at a
 * home, from the home's `api_level`, `api_level_oldest` and `client_floor`
 * (`GET /api/discovery/home`) and the app's own build. The same decision as the home's Go one
 * (`api/internal/compat`); `shared/compat/vectors.json` holds the cases both must agree on.
 */
object ApiLevels {
    /** The oldest home level these apps can use: 0, every home, those from before the contract too. */
    const val MIN = 0

    /** The level these apps were built for. */
    const val BUILT_FOR = 1
}

enum class CompatVerdict(val wire: String) {
    /** Carry on: what the home lacks is hidden through its capabilities. */
    Ok("ok"),

    /** The home is older than the app can use: Update the home. */
    UpdateHome("update_home"),

    /** The home no longer serves this build: Update this app. */
    UpdateApp("update_app"),
}

/** What a home said of the contract. A home from before it says nothing: level 0, no floor. */
data class CompatHome(
    val apiLevel: Int = 0,
    val apiLevelOldest: Int = 0,
    val clientFloor: Map<String, String> = emptyMap(),
)

/** This app's build, as `X-Maktaba-Client` names it, and the levels it was made for. */
data class CompatApp(
    val name: String,
    val platform: String,
    val version: String,
    val minApiLevel: Int = ApiLevels.MIN,
    val builtForApiLevel: Int = ApiLevels.BUILT_FOR,
) {
    /** The floor key of this app: `<app>/<platform>`. */
    val key: String get() = "$name/$platform"

    companion object {
        /** The app an `X-Maktaba-Client` value names (`tv/1.2.0 (androidtv; tv)`). */
        fun fromClientHeader(value: String, minApiLevel: Int = ApiLevels.MIN, builtForApiLevel: Int = ApiLevels.BUILT_FOR): CompatApp {
            val head = value.substringBefore('(').trim()
            val name = head.substringBefore('/').trim().lowercase()
            val version = head.substringAfter('/', "").trim()
            val platform = value.substringAfter('(', "").substringBefore(')').substringBefore(';').trim().lowercase()
            return CompatApp(name, platform, version, minApiLevel, builtForApiLevel)
        }
    }
}

object Compat {
    /**
     * Compares two dotted versions by number: negative, 0 or positive, null when either has no
     * number to read. A missing part is 0 (`1.4` is `1.4.0`); a pre-release suffix ends the
     * number (`1.3.9-beta` is `1.3.9`); a leading `v` is dropped.
     */
    fun compareVersions(a: String, b: String): Int? {
        val pa = parts(a) ?: return null
        val pb = parts(b) ?: return null
        for (i in 0 until maxOf(pa.size, pb.size)) {
            val x = pa.getOrElse(i) { 0 }
            val y = pb.getOrElse(i) { 0 }
            if (x != y) return x.compareTo(y)
        }
        return 0
    }

    private fun parts(v: String): List<Int>? {
        val out = mutableListOf<Int>()
        for (p in v.trim().removePrefix("v").split('.')) {
            val digits = p.takeWhile { it in '0'..'9' }
            if (digits.isEmpty()) break
            out += digits.toIntOrNull() ?: return null
            if (digits.length < p.length) break
        }
        return out.ifEmpty { null }
    }

    /** Whether [app]'s version is below its entry in [floor] (an unreadable version never is). */
    fun belowFloor(floor: Map<String, String>, app: CompatApp): Boolean {
        val min = floor[app.key] ?: return false
        return (compareVersions(app.version, min) ?: return false) < 0
    }

    /** What [app] does at [home]. */
    fun decide(home: CompatHome, app: CompatApp): CompatVerdict = when {
        home.apiLevel < app.minApiLevel -> CompatVerdict.UpdateHome
        app.builtForApiLevel < home.apiLevelOldest -> CompatVerdict.UpdateApp
        belowFloor(home.clientFloor, app) -> CompatVerdict.UpdateApp
        else -> CompatVerdict.Ok
    }
}
