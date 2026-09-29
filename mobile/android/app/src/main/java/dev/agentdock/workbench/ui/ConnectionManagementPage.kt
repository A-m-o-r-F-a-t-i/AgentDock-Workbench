package dev.agentdock.workbench.ui

import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.verticalScroll
import androidx.compose.material3.Button
import androidx.compose.material3.Card
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.OutlinedButton
import androidx.compose.material3.OutlinedTextField
import androidx.compose.material3.Switch
import androidx.compose.material3.Text
import androidx.compose.material3.TextButton
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.setValue
import androidx.compose.ui.Modifier
import androidx.compose.ui.text.input.PasswordVisualTransformation
import androidx.compose.ui.unit.dp
import androidx.lifecycle.compose.collectAsStateWithLifecycle
import dev.agentdock.workbench.model.BridgeOperation
import java.net.URI
import java.text.DateFormat
import java.util.Date

@Composable
fun ConnectionManagementPage(state: WorkbenchUiState, model: WorkbenchViewModel, modifier: Modifier) {
    val resources by model.resources.state.collectAsStateWithLifecycle()
    var endpoint by remember(state.settings.endpoint) { mutableStateOf(state.settings.endpoint) }
    var remoteEnabled by remember(state.settings.remoteEndpointEnabled) { mutableStateOf(state.settings.remoteEndpointEnabled) }
    var bearer by remember { mutableStateOf("") }

    fun reload() {
        model.resources.read("connection-status", "/internal/runtime/status")
        model.resources.read("connection", "/internal/runtime/execution/connection")
        model.resources.read("connection-capabilities", "/internal/runtime/capabilities")
        model.resources.read("oauth-metadata", "/.well-known/oauth-authorization-server")
        model.resources.read("oauth-resource", "/.well-known/oauth-protected-resource/mcp")
    }
    LaunchedEffect(state.settings.endpoint, state.settings.remoteEndpointEnabled) { reload() }

    val status = resources.views["connection-status"]
    val connection = resources.views["connection"]
    val capabilities = resources.views["connection-capabilities"]
    val oauth = resources.views["oauth-metadata"]
    val oauthResource = resources.views["oauth-resource"]
    val oauthData = oauth?.data
    val methods = oauthData?.optJSONArray("code_challenge_methods_supported")
    val pkceSupported = methods != null && (0 until methods.length()).any { methods.optString(it) == "S256" }
    val endpointOrigin = runCatching { URI(endpoint.trim().trimEnd('/')) }.getOrNull()
    val loopback = endpointOrigin?.let(dev.agentdock.workbench.termux.LocalCorePairingManager::loopbackOrigin) == true
    val expiry = state.oauthExpiresAtEpochMs.takeIf { it > 0L }?.let { DateFormat.getDateTimeInstance().format(Date(it)) }
    val connectionData = connection?.data
    val observedAuthorized = connectionData?.optString("state") == "authorized"

    Column(modifier.verticalScroll(rememberScrollState()), verticalArrangement = Arrangement.spacedBy(10.dp)) {
        Text("Core、配对与公网状态", style = MaterialTheme.typography.headlineSmall)
        Text("连接 Origin、客户端凭据、Core 授权观察和公网可达性是四个独立状态；页面不会从其中一项推断其余项。")

        PhoneExecutionPanel(state)

        ResourceSection("连接配置") {
            OutlinedTextField(
                value = endpoint,
                onValueChange = { endpoint = it.take(4096) },
                label = { Text("Core Origin") },
                supportingText = { Text("默认 loopback 可用 HTTP；远程 Origin 必须显式启用并使用 HTTPS。") },
                modifier = Modifier.fillMaxWidth(),
                singleLine = true
            )
            Row(Modifier.fillMaxWidth(), horizontalArrangement = Arrangement.SpaceBetween) {
                Column(Modifier.weight(1f)) {
                    Text("启用远程 Core")
                    Text("关闭时只接受 localhost、127.0.0.0/8 或 IPv6 loopback。", style = MaterialTheme.typography.bodySmall)
                }
                Switch(remoteEnabled, { remoteEnabled = it })
            }
            OutlinedTextField(
                value = bearer,
                onValueChange = { bearer = it.take(8192) },
                label = { Text("手工 Bearer（留空不更改）") },
                supportingText = { Text("一次性输入，不进入 SavedState、DataStore、日志或截图 Fixture。") },
                visualTransformation = PasswordVisualTransformation(),
                modifier = Modifier.fillMaxWidth(),
                singleLine = true
            )
            ResourceActions {
                Button(onClick = {
                    model.saveConnection(endpoint, remoteEnabled, bearer)
                    bearer = ""
                }, enabled = !state.actionBusy && endpoint.isNotBlank() && !state.fixture) { Text("保存并连接") }
                OutlinedButton(onClick = model::clearBearer, enabled = !state.actionBusy && !state.fixture) { Text("删除本机授权") }
                TextButton(onClick = ::reload) { Text("刷新证据") }
            }
            Text("凭据可发送：${state.credentialAvailable}；Core 当前观察为已授权：$observedAuthorized")
            Text("保存的凭据始终与 scheme、host、port 共同加密绑定，切换 Origin 不会携带旧凭据。")
        }

        ResourceSection("自动配对") {
            ResourceActions {
                Button(
                    onClick = model::pairLocalCore,
                    enabled = !state.actionBusy && loopback && !state.fixture && state.settings.endpoint.trimEnd('/') == endpoint.trimEnd('/')
                ) { Text("通过 Termux 安全配对本机 Core") }
                Button(
                    onClick = model::pairRemoteOAuth,
                    enabled = !state.actionBusy && pkceSupported && !state.fixture && state.settings.endpoint.trimEnd('/') == endpoint.trimEnd('/')
                ) { Text("通过浏览器 OAuth/PKCE 配对") }
            }
            Text("本机配对：Android 生成一次性 Keystore RSA 密钥，Termux 只返回 OAEP 密文；Bearer 明文不进入 Intent、参数、日志或持久操作记录。")
            Text("远程配对：动态注册公共客户端，使用 PKCE S256、随机 state 和 127.0.0.1 临时回调；只保存短期访问令牌，不持久保存刷新令牌或客户端秘密。")
            if (state.oauthConfigured) {
                Text("OAuth 已配置：${state.oauthValid}；到期：${expiry ?: state.oauthExpiresAtEpochMs}；issuer：${state.oauthIssuer}")
                if (!state.oauthValid) Text("OAuth 访问令牌已过期或接近过期，不再发送；请重新配对。")
            }
            if (endpoint.trimEnd('/') != state.settings.endpoint.trimEnd('/')) Text("请先保存当前 Origin，再发起配对，避免把授权绑定到未提交地址。")
        }

        ResourceSection("Core 运行与授权观察") {
            ResourceFeedback(status, ::reload)
            status?.data?.let { ResourceObject(it, listOf("ok", "source", "service", "version", "auth_enabled", "oauth_enabled", "nexus_enabled", "browser_enabled", "memory_enabled", "tool_count", "agentdock_home", "default_dir")) }
            ResourceFeedback(connection, ::reload)
            connectionData?.let {
                ResourceObject(it, listOf("state", "summary", "detail", "authorized_clients", "public_reachability", "public_reachability_reason", "updated_at"))
                it.optJSONArray("observed_clients")?.let { clients ->
                    Text("已观察客户端", style = MaterialTheme.typography.titleSmall)
                    for (index in 0 until minOf(clients.length(), 100)) {
                        clients.optJSONObject(index)?.let { client -> ResourceObject(client, listOf("client_id", "client_name", "state", "updated_at")) }
                    }
                }
            }
        }

        ResourceSection("OAuth 与能力探测") {
            ResourceFeedback(oauth, ::reload)
            oauthData?.let { ResourceObject(it, listOf("issuer", "authorization_endpoint", "token_endpoint", "registration_endpoint", "grant_types_supported", "response_types_supported", "code_challenge_methods_supported", "token_endpoint_auth_methods_supported", "resource_indicators_supported")) }
            ResourceFeedback(oauthResource, ::reload)
            oauthResource?.data?.let { ResourceObject(it, listOf("resource", "authorization_servers", "bearer_methods_supported", "scopes_supported")) }
            ResourceFeedback(capabilities, ::reload)
            capabilities?.data?.let { value ->
                ResourceObject(value, listOf("source", "summary", "tool_count", "skill_count", "plugin_count", "mcp_count", "revision"))
            }
            Text("OAuth 元数据不可用时，按钮保持禁用并展示真实读取错误，不回退为不安全的令牌猜测。")
        }

        ResourceSection("公网可达性与 Tunnel 边界") {
            val publicState = connectionData?.optString("public_reachability").orEmpty().ifBlank { "未读取" }
            val publicReason = connectionData?.optString("public_reachability_reason").orEmpty()
            Text("Core 报告的公网可达性：$publicState")
            if (publicReason.isNotBlank()) Text(publicReason)
            Text("当前 Local Control API 没有 Tunnel 创建、修改或撤销写路由。Android 仅展示 Core 观察状态；Tailscale/Cloudflare 等平台连接继续由受控 CLI/平台流程管理。")
            Text("因此，本页不会用一个无效的“公网访问”开关伪装已创建 Tunnel，也不会把 OAuth 授权等同于公网可达。")
        }

        ResourceSection("最近配对操作") {
            val pairing = state.operations.filter { it.operation == "pair_local_core" }.take(20)
            if (pairing.isEmpty()) Text("没有本机配对操作记录。")
            pairing.forEach { PairingOperationCard(it) }
        }
    }
}

@Composable
private fun PairingOperationCard(operation: BridgeOperation) {
    Card(Modifier.fillMaxWidth()) {
        Column(Modifier.padding(12.dp), verticalArrangement = Arrangement.spacedBy(4.dp)) {
            Text("${operation.operation} · ${operation.phase}", style = MaterialTheme.typography.titleSmall)
            Text(operation.operationId)
            if (operation.message.isNotBlank()) Text(operation.message)
            Text("创建：${DateFormat.getDateTimeInstance().format(Date(operation.createdAtEpochMs))}")
        }
    }
}
