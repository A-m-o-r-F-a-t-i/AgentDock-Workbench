package dev.agentdock.workbench.termux

import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertTrue
import org.junit.Test
import java.net.URI

class LocalCorePairingPolicyTest {
    @Test
    fun acceptsAndCanonicalizesLiteralLoopbackOrigins() {
        assertTrue(LocalCorePairingManager.loopbackOrigin(URI("http://localhost")))
        assertTrue(LocalCorePairingManager.loopbackOrigin(URI("http://127.0.0.42:8765")))
        assertTrue(LocalCorePairingManager.loopbackOrigin(URI("http://[::1]")))
        assertTrue(LocalCorePairingManager.loopbackOrigin(URI("https://[0:0:0:0:0:0:0:1]:9443")))

        assertEquals("http://localhost:80", LocalCorePairingManager.canonicalOrigin(URI("http://localhost")))
        assertEquals("http://[::1]:80", LocalCorePairingManager.canonicalOrigin(URI("http://[::1]")))
        assertEquals(
            "https://[0:0:0:0:0:0:0:1]:9443",
            LocalCorePairingManager.canonicalOrigin(URI("https://[0:0:0:0:0:0:0:1]:9443"))
        )
    }

    @Test
    fun rejectsNonLoopbackOrOriginsWithAdditionalComponents() {
        listOf(
            "https://core.example",
            "http://192.168.1.10",
            "http://127.0.0.1/path",
            "http://127.0.0.1?query=1",
            "ftp://127.0.0.1"
        ).forEach { raw ->
            assertFalse(raw, LocalCorePairingManager.loopbackOrigin(URI(raw)))
        }
    }
}
