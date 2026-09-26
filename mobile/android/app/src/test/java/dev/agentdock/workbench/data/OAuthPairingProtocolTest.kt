package dev.agentdock.workbench.data

import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertThrows
import org.junit.Assert.assertTrue
import org.junit.Test
import java.net.URI

class OAuthPairingProtocolTest {
    @Test
    fun pkceMatchesRfc7636Vector() {
        val verifier = "dBjftJeZ4CVP-mB92K27uhbUJU1p1r_wW1gFWFOEjXk"
        assertEquals("E9Melhoa2OwvFrEMTJguCHaoeK1t8URWbuGJSstw-cM", OAuthPairingProtocol.challenge(verifier))
    }

    @Test
    fun trustedEndpointsMustRemainOnIssuerOrigin() {
        val issuer = URI("https://core.example:9443")
        assertEquals(
            URI("https://core.example:9443/authorize"),
            OAuthPairingProtocol.trustedEndpoint("https://core.example:9443/authorize", issuer)
        )
        listOf(
            "http://core.example:9443/authorize",
            "https://other.example:9443/authorize",
            "https://core.example:9443/authorize?next=other",
            "https://user@core.example:9443/authorize"
        ).forEach { raw ->
            assertThrows(IllegalArgumentException::class.java) { OAuthPairingProtocol.trustedEndpoint(raw, issuer) }
        }
    }

    @Test
    fun authorizationUrlCarriesPkceStateAndResource() {
        val url = OAuthPairingProtocol.authorizationUrl(
            URI("https://core.example/authorize"),
            "client-id",
            URI("http://127.0.0.1:43210/oauth/callback"),
            "challenge-value",
            "state-value",
            URI("https://core.example/mcp")
        ).toString()
        assertTrue(url.startsWith("https://core.example/authorize?"))
        assertTrue(url.contains("code_challenge_method=S256"))
        assertTrue(url.contains("state=state-value"))
        assertTrue(url.contains("resource=https%3A%2F%2Fcore.example%2Fmcp"))
    }

    @Test
    fun callbackParserRejectsDuplicatesAndUnknownParameters() {
        val valid = OAuthPairingProtocol.callbackParameters("/oauth/callback?code=abc&state=xyz")
        assertEquals("abc", valid["code"])
        assertEquals("xyz", valid["state"])
        assertThrows(IllegalArgumentException::class.java) {
            OAuthPairingProtocol.callbackParameters("/oauth/callback?code=a&code=b&state=s")
        }
        assertThrows(IllegalArgumentException::class.java) {
            OAuthPairingProtocol.callbackParameters("/oauth/callback?code=a&state=s&token=secret")
        }
        assertThrows(IllegalArgumentException::class.java) {
            OAuthPairingProtocol.callbackParameters("/wrong?code=a&state=s")
        }
    }

    @Test
    fun originComparisonNormalizesDefaultPortsButNotPaths() {
        assertTrue(OAuthPairingProtocol.sameOrigin(URI("https://core.example"), URI("https://core.example:443/path")))
        assertFalse(OAuthPairingProtocol.sameOrigin(URI("https://core.example"), URI("http://core.example")))
        assertFalse(OAuthPairingProtocol.sameOrigin(URI("https://core.example"), URI("https://other.example")))
    }
    @Test
    fun acceptsCoreLongLivedCompatibilityExpiryWithBoundedArithmetic() {
        val now = 1_700_000_000_000L
        val compatibilitySeconds = 999_999L * 24L * 60L * 60L
        assertEquals(now + compatibilitySeconds * 1000L, OAuthPairingProtocol.expiryEpochMs(now, compatibilitySeconds))
        assertThrows(IllegalArgumentException::class.java) { OAuthPairingProtocol.expiryEpochMs(now, 29L) }
        assertThrows(IllegalArgumentException::class.java) { OAuthPairingProtocol.expiryEpochMs(now, 100_000_000_001L) }
        assertThrows(ArithmeticException::class.java) { OAuthPairingProtocol.expiryEpochMs(Long.MAX_VALUE - 1L, 30L) }
    }

    @Test
    fun requiresExactAdvertisedOAuthCapabilities() {
        val metadata = org.json.JSONObject()
            .put("code_challenge_methods_supported", org.json.JSONArray().put("S256"))
            .put("response_types_supported", org.json.JSONArray().put("code"))
        assertTrue(OAuthPairingProtocol.supports(metadata, "code_challenge_methods_supported", "S256"))
        assertTrue(OAuthPairingProtocol.supports(metadata, "response_types_supported", "code"))
        assertFalse(OAuthPairingProtocol.supports(metadata, "response_types_supported", "token"))
        assertFalse(OAuthPairingProtocol.supports(metadata, "missing", "code"))
    }

}
