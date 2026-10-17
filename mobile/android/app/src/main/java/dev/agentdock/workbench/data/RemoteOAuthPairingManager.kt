package dev.agentdock.workbench.data

import org.json.JSONObject
import java.io.ByteArrayOutputStream
import java.io.IOException
import java.net.HttpURLConnection
import java.net.InetAddress
import java.net.Proxy
import java.net.ServerSocket
import java.net.Socket
import java.net.SocketTimeoutException
import java.net.URI
import java.net.URLEncoder
import java.nio.charset.StandardCharsets
import java.security.MessageDigest
import java.security.SecureRandom
import java.util.Base64
import java.util.Locale

data class OAuthPendingSession internal constructor(
    val origin: URI,
    val authorizationUrl: URI,
    internal val server: ServerSocket,
    internal val state: String,
    internal val verifier: String,
    internal val clientId: String,
    internal val redirectUri: URI,
    internal val tokenEndpoint: URI,
    internal val resource: URI,
    internal val issuer: URI,
    internal val startedAtMonotonicNanos: Long
)

internal sealed interface OAuthCallbackDecision {
    data object Ignore : OAuthCallbackDecision
    data class Code(val value: String) : OAuthCallbackDecision
    data class Error(val value: String) : OAuthCallbackDecision
}

internal class OAuthAuthorizationRejectedException(message: String) : IOException(message)

internal class OAuthCallbackBudget(private val maximumConnections: Int = 16) {
    init { require(maximumConnections > 0) }

    var acceptedConnections: Int = 0
        private set

    fun mayWait(nowMonotonicNanos: Long, deadlineMonotonicNanos: Long): Boolean =
        deadlineMonotonicNanos - nowMonotonicNanos > 0L && acceptedConnections < maximumConnections

    fun recordAcceptedConnection() {
        check(acceptedConnections < maximumConnections) { "OAuth loopback 连接数超过上限" }
        acceptedConnections++
    }
}

internal object OAuthPairingProtocol {
    private val random = SecureRandom()

    fun randomUrlToken(bytes: Int = 32): String {
        require(bytes in 24..64)
        val value = ByteArray(bytes)
        random.nextBytes(value)
        return Base64.getUrlEncoder().withoutPadding().encodeToString(value)
    }

    fun challenge(verifier: String): String {
        require(verifier.length in 43..128 && verifier.all { it.isLetterOrDigit() || it in "-._~" })
        val digest = MessageDigest.getInstance("SHA-256").digest(verifier.toByteArray(StandardCharsets.US_ASCII))
        return Base64.getUrlEncoder().withoutPadding().encodeToString(digest)
    }

    fun sameOrigin(left: URI, right: URI): Boolean = runCatching {
        fun tuple(value: URI): Triple<String, String, Int> {
            val scheme = value.scheme?.lowercase(Locale.ROOT)
            require(scheme in setOf("http", "https") && value.userInfo == null)
            val host = requireNotNull(value.host).removeSurrounding("[", "]").lowercase(Locale.ROOT)
            val port = if (value.port < 0) if (scheme == "https") 443 else 80 else value.port
            require(port in 1..65535)
            return Triple(requireNotNull(scheme), host, port)
        }
        tuple(left) == tuple(right)
    }.getOrDefault(false)

    fun trustedEndpoint(raw: String, issuer: URI): URI {
        require(raw.length in 1..4096)
        val value = URI(raw)
        require(sameOrigin(value, issuer) && value.userInfo == null && value.fragment == null && value.query == null &&
            value.path.orEmpty().startsWith('/')) { "OAuth 端点必须与 issuer 同源且不含凭据、查询或片段" }
        return value
    }

    fun authorizationUrl(
        endpoint: URI,
        clientId: String,
        redirectUri: URI,
        challenge: String,
        state: String,
        resource: URI
    ): URI {
        val query = linkedMapOf(
            "response_type" to "code",
            "client_id" to clientId,
            "redirect_uri" to redirectUri.toString(),
            "code_challenge" to challenge,
            "code_challenge_method" to "S256",
            "state" to state,
            "resource" to resource.toString()
        ).entries.joinToString("&") { (key, value) -> "${encode(key)}=${encode(value)}" }
        return URI(endpoint.toString() + "?" + query)
    }

    fun callbackParameters(target: String): Map<String, String> {
        require(target.length in 1..8192)
        val uri = URI(target)
        require(uri.path == "/oauth/callback" && uri.fragment == null)
        val result = LinkedHashMap<String, String>()
        for (part in uri.rawQuery.orEmpty().split('&').filter(String::isNotBlank)) {
            val separator = part.indexOf('=')
            val key = decode(if (separator < 0) part else part.substring(0, separator))
            val value = decode(if (separator < 0) "" else part.substring(separator + 1))
            require(key in setOf("code", "state", "error", "error_description") && key !in result) { "OAuth 回调参数重复或未声明" }
            require(value.length <= 4096)
            result[key] = value
        }
        return result
    }

    fun callbackDecision(values: Map<String, String>, expectedState: String): OAuthCallbackDecision {
        require(expectedState.isNotBlank())
        if (values["state"] != expectedState) return OAuthCallbackDecision.Ignore

        val code = values["code"]
        val error = values["error"]
        if ((code == null) == (error == null) || (code != null && "error_description" in values)) {
            return OAuthCallbackDecision.Error("invalid_response")
        }
        if (error != null) {
            val safe = error.filter { it.code in 33..126 }.take(128).ifBlank { "oauth_error" }
            return OAuthCallbackDecision.Error(safe)
        }
        val accepted = code.orEmpty()
        if (accepted.length !in 1..4096 || accepted.any { it.code !in 33..126 }) {
            return OAuthCallbackDecision.Error("invalid_response")
        }
        return OAuthCallbackDecision.Code(accepted)
    }

    fun form(values: Map<String, String>): ByteArray = values.entries.joinToString("&") { (key, value) ->
        "${encode(key)}=${encode(value)}"
    }.toByteArray(StandardCharsets.UTF_8)

    fun expiryEpochMs(nowEpochMs: Long, expiresInSeconds: Long): Long {
        require(expiresInSeconds in 30..MAX_EXPIRES_IN_SECONDS) { "OAuth expires_in 无效" }
        return Math.addExact(nowEpochMs, Math.multiplyExact(expiresInSeconds, 1000L))
    }

    fun supports(metadata: JSONObject, field: String, expected: String): Boolean {
        val values = metadata.optJSONArray(field) ?: return false
        return (0 until values.length()).any { values.optString(it) == expected }
    }

    private fun encode(value: String) = URLEncoder.encode(value, StandardCharsets.UTF_8.name())
    private fun decode(value: String): String = java.net.URLDecoder.decode(value, StandardCharsets.UTF_8.name())

    private const val MAX_EXPIRES_IN_SECONDS = 100_000_000_000L
}

class RemoteOAuthPairingManager(private val credentials: CredentialStore) {
    fun begin(origin: URI): OAuthPendingSession {
        val metadata = getJson(origin.resolve("/.well-known/oauth-authorization-server"), MAX_METADATA_BYTES)
        val issuer = URI(metadata.getString("issuer"))
        require(OAuthPairingProtocol.sameOrigin(origin, issuer) && issuer.userInfo == null && issuer.query == null &&
            issuer.fragment == null && issuer.path.orEmpty() in setOf("", "/")) {
            "OAuth issuer 与配置的 Core Origin 不一致"
        }
        val authorization = OAuthPairingProtocol.trustedEndpoint(metadata.getString("authorization_endpoint"), issuer)
        val token = OAuthPairingProtocol.trustedEndpoint(metadata.getString("token_endpoint"), issuer)
        val registration = OAuthPairingProtocol.trustedEndpoint(metadata.getString("registration_endpoint"), issuer)
        require(OAuthPairingProtocol.supports(metadata, "code_challenge_methods_supported", "S256")) { "Core OAuth 未声明 PKCE S256" }
        require(OAuthPairingProtocol.supports(metadata, "response_types_supported", "code") &&
            OAuthPairingProtocol.supports(metadata, "grant_types_supported", "authorization_code") &&
            OAuthPairingProtocol.supports(metadata, "token_endpoint_auth_methods_supported", "none") &&
            metadata.optBoolean("resource_indicators_supported")) {
            "Core OAuth 元数据缺少公共客户端、授权码或资源指示器能力"
        }

        val resourceMetadata = getJson(origin.resolve("/.well-known/oauth-protected-resource/mcp"), MAX_METADATA_BYTES)
        val resource = URI(resourceMetadata.getString("resource"))
        require(OAuthPairingProtocol.sameOrigin(resource, issuer) && resource.path == "/mcp" && resource.query == null && resource.fragment == null) {
            "受保护资源与 OAuth issuer 不一致"
        }
        val authorizationServers = resourceMetadata.optJSONArray("authorization_servers")
        require(authorizationServers != null && (0 until authorizationServers.length()).any {
            runCatching {
                val server = URI(authorizationServers.getString(it))
                OAuthPairingProtocol.sameOrigin(server, issuer) && server.path.orEmpty() in setOf("", "/") &&
                    server.query == null && server.fragment == null && server.userInfo == null
            }.getOrDefault(false)
        }) { "受保护资源未声明当前 issuer" }
        require(OAuthPairingProtocol.supports(resourceMetadata, "bearer_methods_supported", "header")) {
            "受保护资源未声明 Authorization Header Bearer 方法"
        }

        val server = ServerSocket(0, 8, InetAddress.getByName("127.0.0.1"))
        server.reuseAddress = false
        val redirect = URI("http://127.0.0.1:${server.localPort}/oauth/callback")
        try {
            val registrationResult = postJson(
                registration,
                JSONObject()
                    .put("client_name", "AgentDock Workbench Android")
                    .put("redirect_uris", org.json.JSONArray().put(redirect.toString()))
                    .put("token_endpoint_auth_method", "none")
                    .put("grant_types", org.json.JSONArray().put("authorization_code"))
                    .put("response_types", org.json.JSONArray().put("code")),
                MAX_METADATA_BYTES
            )
            val clientId = registrationResult.getString("client_id")
            require(clientId.length in 1..512 && clientId.all { it.code in 33..126 }) { "OAuth client_id 无效" }
            require(registrationResult.optString("token_endpoint_auth_method") == "none") {
                "OAuth 注册结果改变了公共客户端认证方式"
            }
            val registeredRedirects = registrationResult.optJSONArray("redirect_uris")
            require(registeredRedirects != null && (0 until registeredRedirects.length()).any {
                registeredRedirects.optString(it) == redirect.toString()
            }) { "OAuth 注册结果未保留本次 loopback 回调" }
            val verifier = OAuthPairingProtocol.randomUrlToken(48)
            val state = OAuthPairingProtocol.randomUrlToken(32)
            return OAuthPendingSession(
                origin = origin,
                authorizationUrl = OAuthPairingProtocol.authorizationUrl(
                    authorization, clientId, redirect, OAuthPairingProtocol.challenge(verifier), state, resource
                ),
                server = server,
                state = state,
                verifier = verifier,
                clientId = clientId,
                redirectUri = redirect,
                tokenEndpoint = token,
                resource = resource,
                issuer = issuer,
                startedAtMonotonicNanos = System.nanoTime()
            )
        } catch (error: Throwable) {
            runCatching { server.close() }
            throw error
        }
    }

    fun awaitAndStore(session: OAuthPendingSession): OAuthCredentialStatus {
        try {
            val code = awaitAuthorizationCode(session)
            val token = postForm(
                session.tokenEndpoint,
                linkedMapOf(
                    "grant_type" to "authorization_code",
                    "code" to code,
                    "redirect_uri" to session.redirectUri.toString(),
                    "client_id" to session.clientId,
                    "code_verifier" to session.verifier,
                    "resource" to session.resource.toString()
                ),
                MAX_TOKEN_BYTES
            )
            require(token.optString("token_type").equals("Bearer", ignoreCase = true)) { "OAuth token_type 不是 Bearer" }
            val access = token.getString("access_token")
            require(access.length in 1..16_384 && access.all { it.code in 33..126 }) { "OAuth access_token 无效" }
            val expiresIn = token.optLong("expires_in", 0L)
            val expiresAt = OAuthPairingProtocol.expiryEpochMs(System.currentTimeMillis(), expiresIn)
            credentials.putOAuth(session.origin, access, expiresAt, session.clientId, session.issuer.toString())
            return credentials.oauthStatus(session.origin)
        } finally {
            runCatching { session.server.close() }
        }
    }

    fun cancel(session: OAuthPendingSession) {
        runCatching { session.server.close() }
    }

    private fun awaitAuthorizationCode(session: OAuthPendingSession): String {
        val deadline = session.startedAtMonotonicNanos + CALLBACK_TIMEOUT_NANOS
        session.server.soTimeout = 1000
        val budget = OAuthCallbackBudget()
        while (budget.mayWait(System.nanoTime(), deadline)) {
            val socket = try {
                session.server.accept()
            } catch (_: SocketTimeoutException) {
                continue
            }
            budget.recordAcceptedConnection()
            val code = try {
                socket.use { client -> handleCallbackClient(client, session) }
            } catch (error: OAuthAuthorizationRejectedException) {
                throw error
            } catch (_: IOException) {
                null
            } catch (_: IllegalArgumentException) {
                null
            }
            if (code != null) return code
        }
        error("OAuth loopback 回调等待超时")
    }

    private fun handleCallbackClient(client: Socket, session: OAuthPendingSession): String? {
        if (!client.inetAddress.isLoopbackAddress) return null
        client.soTimeout = 5000
        val request = readRequest(client)
        val parts = request.first().split(' ')
        if (parts.size != 3 || parts[0] != "GET" || parts[2] !in setOf("HTTP/1.1", "HTTP/1.0")) {
            respond(client, 400, "Invalid OAuth callback")
            return null
        }
        val target = parts[1]
        val path = runCatching { URI(target).path }.getOrNull()
        if (path != "/oauth/callback") {
            respond(client, 404, "Not found")
            return null
        }
        val values = runCatching { OAuthPairingProtocol.callbackParameters(target) }.getOrElse {
            respond(client, 400, "Invalid OAuth callback")
            return null
        }
        return when (val decision = OAuthPairingProtocol.callbackDecision(values, session.state)) {
            OAuthCallbackDecision.Ignore -> {
                respond(client, 400, "OAuth state mismatch")
                null
            }
            is OAuthCallbackDecision.Error -> {
                respond(client, 400, "Authorization was not completed")
                throw OAuthAuthorizationRejectedException("OAuth 授权失败：${decision.value}")
            }
            is OAuthCallbackDecision.Code -> {
                respond(client, 200, "AgentDock Workbench authorization completed. Return to the app.")
                decision.value
            }
        }
    }

    private fun readRequest(socket: Socket): List<String> {
        val input = socket.getInputStream()
        val lines = ArrayList<String>()
        var total = 0
        while (lines.size < 100) {
            val output = ByteArrayOutputStream()
            while (output.size() <= 4096) {
                val value = input.read()
                if (value < 0) break
                total++
                require(total <= 16 * 1024) { "OAuth 回调请求头超过上限" }
                if (value == '\n'.code) break
                if (value != '\r'.code) output.write(value)
            }
            require(output.size() <= 4096) { "OAuth 回调请求行超过上限" }
            val line = output.toString(StandardCharsets.US_ASCII.name())
            lines += line
            if (line.isEmpty()) break
        }
        require(lines.isNotEmpty() && lines.last().isEmpty()) { "OAuth 回调请求不完整" }
        return lines
    }

    private fun respond(socket: Socket, status: Int, text: String) {
        val body = ("<!doctype html><meta charset=\"utf-8\"><title>AgentDock Workbench</title><p>" +
            text.replace("&", "&amp;").replace("<", "&lt;").replace(">", "&gt;") + "</p>").toByteArray(StandardCharsets.UTF_8)
        val reason = if (status == 200) "OK" else if (status == 404) "Not Found" else "Bad Request"
        socket.getOutputStream().use { output ->
            output.write(("HTTP/1.1 $status $reason\r\nContent-Type: text/html; charset=utf-8\r\n" +
                "Cache-Control: no-store\r\nConnection: close\r\nContent-Length: ${body.size}\r\n\r\n").toByteArray(StandardCharsets.US_ASCII))
            output.write(body)
            output.flush()
        }
    }

    private fun getJson(uri: URI, maximum: Int): JSONObject = requestJson("GET", uri, null, null, maximum)
    private fun postJson(uri: URI, body: JSONObject, maximum: Int): JSONObject =
        requestJson("POST", uri, "application/json; charset=utf-8", body.toString().toByteArray(StandardCharsets.UTF_8), maximum)
    private fun postForm(uri: URI, values: Map<String, String>, maximum: Int): JSONObject =
        requestJson("POST", uri, "application/x-www-form-urlencoded; charset=utf-8", OAuthPairingProtocol.form(values), maximum)

    private fun requestJson(method: String, uri: URI, contentType: String?, body: ByteArray?, maximum: Int): JSONObject {
        require(uri.scheme in setOf("http", "https") && uri.userInfo == null && uri.fragment == null)
        val connection = uri.toURL().openConnection(Proxy.NO_PROXY) as HttpURLConnection
        try {
            connection.requestMethod = method
            connection.instanceFollowRedirects = false
            connection.useCaches = false
            connection.connectTimeout = 10_000
            connection.readTimeout = 15_000
            connection.setRequestProperty("Accept", "application/json")
            connection.setRequestProperty("Cache-Control", "no-store")
            if (body != null) {
                require(body.size <= 256 * 1024)
                connection.doOutput = true
                connection.setFixedLengthStreamingMode(body.size)
                connection.setRequestProperty("Content-Type", requireNotNull(contentType))
                connection.outputStream.use { it.write(body) }
            }
            val status = connection.responseCode
            if (status in 300..399) throw IOException("OAuth 端点重定向已拒绝")
            if (status !in 200..299) throw IOException("OAuth 端点返回 HTTP $status")
            val mediaType = connection.contentType.orEmpty().substringBefore(';').trim().lowercase(Locale.ROOT)
            require(mediaType == "application/json" || mediaType == "application/json-seq" || mediaType.endsWith("+json")) { "OAuth 端点未返回 JSON" }
            val bytes = connection.inputStream.use { input ->
                val output = ByteArrayOutputStream(minOf(maximum, 8192))
                val buffer = ByteArray(8192)
                while (true) {
                    val read = input.read(buffer)
                    if (read < 0) break
                    require(output.size() + read <= maximum) { "OAuth 响应超过大小上限" }
                    output.write(buffer, 0, read)
                }
                output.toByteArray()
            }
            return JSONObject(bytes.toString(StandardCharsets.UTF_8))
        } finally {
            connection.disconnect()
        }
    }

    companion object {
        private const val MAX_METADATA_BYTES = 1024 * 1024
        private const val MAX_TOKEN_BYTES = 64 * 1024
        private const val CALLBACK_TIMEOUT_NANOS = 5L * 60L * 1_000_000_000L
    }
}
