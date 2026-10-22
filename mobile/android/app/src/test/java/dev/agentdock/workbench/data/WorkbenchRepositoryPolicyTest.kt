package dev.agentdock.workbench.data

import dev.agentdock.workbench.model.NodeHealth
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertTrue
import org.junit.Test
import java.io.IOException

class WorkbenchRepositoryPolicyTest {
    @Test fun retriesOnlyTransientRefreshReads() {
        assertTrue(retryableRefreshRead(IOException("connection reset")))
        assertTrue(retryableRefreshRead(CoreRequestException(503, "HTTP_503", "unavailable")))
        assertTrue(retryableRefreshRead(CoreRequestException(429, "HTTP_429", "busy")))
        assertTrue(retryableRefreshRead(CoreRequestException(409, "BUSY", "retry", retryable = true)))

        assertFalse(retryableRefreshRead(CoreRequestException(401, "UNAUTHORIZED", "denied")))
        assertFalse(retryableRefreshRead(CoreRequestException(404, "NOT_FOUND", "missing")))
        assertFalse(retryableRefreshRead(IllegalArgumentException("invalid response")))
    }

    @Test fun optionalEndpointFailuresDoNotMislabelCoreHealth() {
        assertEquals(NodeHealth.Healthy, refreshCoreHealth(true, true, true))
        assertEquals(NodeHealth.Degraded, refreshCoreHealth(true, false, true))
        assertEquals(NodeHealth.Degraded, refreshCoreHealth(true, true, false))
        assertEquals(NodeHealth.Unknown, refreshCoreHealth(false, true, true))
    }
}
