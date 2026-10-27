package com.hamzalabs.maktaba.desktop

import com.hamzalabs.maktaba.core.session.FoundHome
import com.hamzalabs.maktaba.core.session.MAKTABA_SERVICE
import com.hamzalabs.maktaba.core.session.dedupeHomes
import com.hamzalabs.maktaba.core.session.foundHome
import kotlinx.coroutines.channels.awaitClose
import kotlinx.coroutines.flow.Flow
import kotlinx.coroutines.flow.callbackFlow
import java.net.InetAddress
import javax.jmdns.JmmDNS
import javax.jmdns.ServiceEvent
import javax.jmdns.ServiceInfo
import javax.jmdns.ServiceListener

/** The service type as JmDNS names it: `_maktaba._tcp.local.` */
const val MDNS_TYPE = "$MAKTABA_SERVICE.local."

/** An announcement's TXT record as the shared rules read it (`foundHome`). */
fun txtOf(info: ServiceInfo): Map<String, String> = buildMap {
    val names = info.propertyNames
    while (names.hasMoreElements()) {
        val k = names.nextElement()
        info.getPropertyString(k)?.let { put(k.lowercase(), it) }
    }
}

/** The address to reach an announcement at: IPv4 first (a link-local IPv6 needs its zone). */
fun addressOf(info: ServiceInfo): InetAddress? = info.inet4Addresses.firstOrNull() ?: info.inet6Addresses.firstOrNull()

/**
 * The homes announcing themselves on this computer's networks, as they come and go (mDNS /
 * DNS-SD on every interface: JmmDNS). Only `role=home`, one row per home (the same rules as
 * the Android and Apple apps: `foundHome`, `dedupeHomes`).
 */
fun discoverHomes(): Flow<List<FoundHome>> = callbackFlow {
    val seen = linkedMapOf<String, FoundHome>()
    fun publish() = trySend(dedupeHomes(seen.values.toList()))
    val listener = object : ServiceListener {
        override fun serviceAdded(event: ServiceEvent) {
            // asks for the details; serviceResolved follows
            event.dns.requestServiceInfo(event.type, event.name, true)
        }

        override fun serviceRemoved(event: ServiceEvent) {
            synchronized(seen) {
                seen.remove(event.name)
                publish()
            }
        }

        override fun serviceResolved(event: ServiceEvent) {
            val info = event.info ?: return
            val home = foundHome(event.name, addressOf(info), info.port, txtOf(info)) ?: return
            synchronized(seen) {
                seen[event.name] = home
                publish()
            }
        }
    }
    val dns = JmmDNS.Factory.getInstance()
    dns.addServiceListener(MDNS_TYPE, listener)
    publish()
    awaitClose {
        dns.removeServiceListener(MDNS_TYPE, listener)
    }
}
