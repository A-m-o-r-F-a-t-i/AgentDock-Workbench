package dev.agentdock.workbench.ui

import dev.agentdock.workbench.model.BridgeOperation
import dev.agentdock.workbench.model.WorkbenchScreen
import org.junit.Assert.assertEquals
import org.junit.Assert.assertTrue
import org.junit.Test

class CoreFailurePolicyTest {
    private fun operation(
        operation: String,
        phase: String = "succeeded",
        result: String = "",
        updatedAt: Long = 1_000L
    ) = BridgeOperation(
        operationId = "op_$operation$updatedAt",
        requestId = "req_$updatedAt",
        nonce = "nonce_$updatedAt",
        operation = operation,
        phase = phase,
        createdAtEpochMs = updatedAt,
        updatedAtEpochMs = updatedAt,
        resultJson = result
    )

    @Test fun missingLocalCoreUsesLatestSuccessfulStatus() {
        val advice = CoreFailurePolicy.advise(
            errors = listOf("Failed to connect to /127.0.0.1:8765"),
            operations = listOf(operation("status", result = """{"version":"","desired_state":"stopped","healthy":false,"manifest_trust_ready":true}""")),
            endpoint = "http://127.0.0.1:8765",
            desiredState = "stopped"
        )
        assertEquals("本机 Core 尚未安装", advice.title)
        assertEquals(WorkbenchScreen.InstallUpdate, advice.destination)
        assertTrue(advice.detail.contains("官方签名源"))
    }

    @Test fun missingTrustKeyRequiresCompleteBridgeBundle() {
        val advice = CoreFailurePolicy.advise(
            errors = listOf("connection refused"),
            operations = listOf(operation("probe", result = """{"version":"","desired_state":"stopped","manifest_trust_ready":false}""")),
            endpoint = "http://localhost:8765",
            desiredState = "stopped"
        )
        assertEquals("本机 Core 尚未安装", advice.title)
        assertTrue(advice.detail.contains("完整 Termux 桥包"))
    }

    @Test fun installedStoppedCoreIsDistinguishedFromMissingCore() {
        val advice = CoreFailurePolicy.advise(
            errors = listOf("ConnectException: connection refused"),
            operations = listOf(operation("status", result = """{"version":"1.1.9","desired_state":"stopped","healthy":false}""")),
            endpoint = "http://127.0.0.1:8765",
            desiredState = "running"
        )
        assertEquals("本机 Core 已停止", advice.title)
        assertTrue(advice.detail.contains("1.1.9"))
    }

    @Test fun unauthorizedCoreRoutesToPairing() {
        val advice = CoreFailurePolicy.advise(
            errors = listOf("UNAUTHORIZED (HTTP 401)"),
            operations = emptyList(),
            endpoint = "https://core.example.test",
            desiredState = "running"
        )
        assertEquals("Core 凭据未授权", advice.title)
        assertEquals(WorkbenchScreen.CoreConnections, advice.destination)
    }

    @Test fun newerSuccessfulInstallInvalidatesStaleMissingStatus() {
        val advice = CoreFailurePolicy.advise(
            errors = listOf("Failed to connect to /127.0.0.1:8765"),
            operations = listOf(
                operation("install", updatedAt = 2_000L),
                operation("status", result = """{"version":"","desired_state":"running","healthy":false}""", updatedAt = 1_000L)
            ),
            endpoint = "http://127.0.0.1:8765",
            desiredState = "running"
        )
        assertEquals("本机 Core 启动未完成", advice.title)
    }

    @Test fun remoteConnectionFailureUsesConnectionManagement() {
        val advice = CoreFailurePolicy.advise(
            errors = listOf("java.net.ConnectException: failed to connect"),
            operations = emptyList(),
            endpoint = "https://core.example.test",
            desiredState = "running"
        )
        assertEquals("无法连接 Core", advice.title)
        assertEquals(WorkbenchScreen.CoreConnections, advice.destination)
    }

    @Test fun unrelatedErrorsKeepGenericDiagnosis() {
        val advice = CoreFailurePolicy.advise(
            errors = listOf("unexpected response shape"),
            operations = emptyList(),
            endpoint = "http://127.0.0.1:8765",
            desiredState = "running"
        )
        assertEquals("Core 数据读取失败", advice.title)
        assertEquals(null, advice.destination)
    }
}
