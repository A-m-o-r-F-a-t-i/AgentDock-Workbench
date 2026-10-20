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
        model.resources.read("oauth-metadata", "/.well-known/oauth-authorization-server")
        model.resources.read("oauth-resource", "/.well-known/oauth-protected-resource/mcp")
    }
    LaunchedEffect(state.settings.endpoint, state.settings.remoteEndpointEnabled) { reload() }

    val status = resources.views["connection-status"]
    val connection = resources.views["connection"]
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
        Text("连接管理", style = MaterialTheme.typography.headlineSmall)
        Text("连接本机或远程节点，管理授权。")

        PhoneExecutionPanel(state)

        ResourceSection("连接配置") {
            OutlinedTextField(
                value = endpoint,
                onValueChange = { endpoint = it.take(4096) },
                label = { Text("节点地址") },
                supportingText = { Text("本机地址可使用 HTTP，远程地址需要 HTTPS。") },
                modifier = Modifier.fillMaxWidth(),
                singleLine = true
            )
            Row(Modifier.fillMaxWidth(), horizontalArrangement = Arrangement.SpaceBetween) {
                Column(Modifier.weight(1f)) {
                    Text("连接远程节点")
                    Text("关闭后仅连接此设备上的节点。", style = MaterialTheme.typography.bodySmall)
                }
                Switch(remoteEnabled, { remoteEnabled = it })
            }
            OutlinedTextField(
                value = bearer,
                onValueChange = { bearer = it.take(8192) },
                label = { Text("手工 Bearer（留空不更改）") },
                supportingText = { Text("输入内容会加密保存，不显示在日志中。") },
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
                TextButton(onClick = ::reload) { Text("刷新状态") }
            }
            Text("本机凭据：${if (state.credentialAvailable) "已保存" else "未配置"} · 节点授权：${if (observedAuthorized) "已确认" else "待确认"}")
            TechnicalDetails("授权说明") { Text("凭据与节点地址加密绑定，切换地址不会携带旧凭据。") }
        }

        ResourceSection("自动配对") {
            ResourceActions {
                Button(
                    onClick = model::pairLocalCore,
                    enabled = !state.actionBusy && loopback && !state.fixture && state.settings.endpoint.trimEnd('/') == endpoint.trimEnd('/')
                ) { Text("配对本机节点") }
                Button(
                    onClick = model::pairRemoteOAuth,
                    enabled = !state.actionBusy && pkceSupported && !state.fixture && state.settings.endpoint.trimEnd('/') == endpoint.trimEnd('/')
                ) { Text("浏览器授权") }
            }
            TechnicalDetails("配对说明") {
                Text("本机通过 Termux 交换加密凭据。远程授权使用 OAuth 与 PKCE，只保存短期访问令牌。")
            }
            if (state.oauthConfigured) {
                Text("OAuth 已配置：${state.oauthValid}；到期：${expiry ?: state.oauthExpiresAtEpochMs}；issuer：${state.oauthIssuer}")
                if (!state.oauthValid) Text("OAuth 访问令牌已过期或接近过期，不再发送；请重新配对。")
            }
            if (endpoint.trimEnd('/') != state.settings.endpoint.trimEnd('/')) Text("请先保存节点地址，再开始配对。")
        }

        ResourceSection("节点状态") {
            ResourceFeedback(status, ::reload)
            status?.data?.let { value ->
                ResourceObject(value, listOf(
                    "ok", "source", "service", "version", "runtime_api", "runtime_contract_revision",
                    "minimum_client_revision", "auth_enabled", "nexus_enabled", "browser_enabled",
                    "memory_enabled", "tool_count", "agentdock_home", "agentdock_default_dir", "path_model"
                ))
                value.optJSONObject("external_agent_access")?.let {
                    ResourceObject(it, listOf("schema_version", "protocol", "local_endpoint", "public_endpoint", "auth_mode", "credentials_included"))
                }
                value.optJSONObject("android_executor")?.let {
                    ResourceObject(it, listOf("connected", "protocol", "node_id", "inflight"))
                }
            }
            Text(state.snapshot.managementCompatibilityMessage)
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

        ResourceSection("连接诊断") {
            ResourceFeedback(oauth, ::reload)
            oauthData?.let { ResourceObject(it, listOf("issuer", "authorization_endpoint", "token_endpoint", "registration_endpoint", "grant_types_supported", "response_types_supported", "code_challenge_methods_supported", "token_endpoint_auth_methods_supported", "resource_indicators_supported")) }
            ResourceFeedback(oauthResource, ::reload)
            oauthResource?.data?.let { ResourceObject(it, listOf("resource", "authorization_servers", "bearer_methods_supported", "scopes_supported")) }
            Text("授权服务不可用时，请先处理上方连接错误。能力库存由各管理页面按需读取，不在连接页构建完整 Context。")
        }

        ResourceSection("公网访问") {
            val publicState = connectionData?.optString("public_reachability").orEmpty().ifBlank { "未读取" }
            val publicReason = connectionData?.optString("public_reachability_reason").orEmpty()
            Text("公网状态：$publicState")
            if (publicReason.isNotBlank()) Text(publicReason)
            Text("此处只查看公网状态。请在 Tailscale 或 Cloudflare 等隧道服务中管理公网连接。")
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
            Text("${userOperation(operation.operation)} · ${userStatus(operation.phase)}", style = MaterialTheme.typography.titleSmall)
            TechnicalDetails("操作标识") { Text(operation.operationId) }
            if (operation.message.isNotBlank()) Text(operation.message)
            Text("创建：${DateFormat.getDateTimeInstance().format(Date(operation.createdAtEpochMs))}")
        }
    }
}
