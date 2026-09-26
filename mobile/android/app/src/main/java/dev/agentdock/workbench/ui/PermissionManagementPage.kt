package dev.agentdock.workbench.ui

import androidx.compose.foundation.horizontalScroll
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.weight
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.verticalScroll
import androidx.compose.material3.Button
import androidx.compose.material3.Checkbox
import androidx.compose.material3.FilterChip
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.OutlinedButton
import androidx.compose.material3.Switch
import androidx.compose.material3.Text
import androidx.compose.material3.TextButton
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.saveable.rememberSaveable
import androidx.compose.runtime.setValue
import androidx.compose.ui.Modifier
import androidx.compose.ui.unit.dp
import androidx.lifecycle.compose.collectAsStateWithLifecycle
import dev.agentdock.workbench.data.ManagementContract
import dev.agentdock.workbench.data.PermissionDraft
import dev.agentdock.workbench.data.WorkbenchCommands
import org.json.JSONObject

@Composable
fun PermissionManagementPage(state: WorkbenchUiState, model: WorkbenchViewModel, modifier: Modifier) {
    val resources by model.resources.state.collectAsStateWithLifecycle()
    val prompt = rememberCommandPrompt(model)
    var scope by rememberSaveable { mutableStateOf("global") }
    var scopeId by rememberSaveable { mutableStateOf("") }
    var mode by rememberSaveable { mutableStateOf("rules") }
    var confirmFull by rememberSaveable { mutableStateOf(false) }
    var customEnabled by rememberSaveable { mutableStateOf(false) }
    var inheritSettings by rememberSaveable { mutableStateOf(false) }
    var filesystem by rememberSaveable { mutableStateOf("write") }
    var network by rememberSaveable { mutableStateOf("allow") }
    var boundary by rememberSaveable { mutableStateOf("none") }
    var approvalMode by rememberSaveable { mutableStateOf("on-request") }
    var reviewer by rememberSaveable { mutableStateOf("user") }
    var fileWrites by rememberSaveable { mutableStateOf(true) }
    var commands by rememberSaveable { mutableStateOf(true) }
    var networkApproval by rememberSaveable { mutableStateOf(true) }
    var mcp by rememberSaveable { mutableStateOf(true) }
    var management by rememberSaveable { mutableStateOf(true) }
    var other by rememberSaveable { mutableStateOf(false) }

    fun validSelection() = scope == "global" || runCatching { ManagementContract.id(scopeId); true }.getOrDefault(false)
    fun permissionPath(): String = buildString {
        append("/internal/runtime/permissions/effective")
        if (scope == "workspace" && scopeId.isNotBlank()) append("?workspace_id=").append(ManagementContract.id(scopeId))
        if (scope == "conversation" && scopeId.isNotBlank()) append("?conversation_id=").append(ManagementContract.id(scopeId))
    }
    fun reload() {
        if (validSelection()) model.resources.read("permission", permissionPath())
        else model.showNotice("请选择或输入有效的工作区／对话 ID。")
    }
    LaunchedEffect(state.settings.endpoint, state.settings.remoteEndpointEnabled, scope, scopeId) {
        if (validSelection()) reload()
    }
    val rawView = resources.views["permission"]
    val view = rawView?.takeIf { validSelection() && it.sourcePath == permissionPath() }
    val envelope = view?.data
    val effective = envelope?.optJSONObject("effective") ?: envelope
    val policy = envelope?.optJSONObject("policy")
    val revision = effective?.optLong("revision", policy?.optLong("revision", -1) ?: -1) ?: -1
    val configured = effective?.optJSONObject("configured_settings") ?: effective?.optJSONObject("settings")

    LaunchedEffect(view?.data?.toString()) {
        val current = effective ?: return@LaunchedEffect
        mode = current.optString("mode", mode).takeIf { it in setOf("readonly", "rules", "full") } ?: mode
        customEnabled = current.optBoolean("custom_permissions_enabled", customEnabled)
        val value = configured ?: return@LaunchedEffect
        val profile = value.optJSONObject("permission_profile")
        filesystem = profile?.optString("filesystem", filesystem).orEmpty().ifBlank { filesystem }
        network = profile?.optString("network", network).orEmpty().ifBlank { network }
        boundary = profile?.optString("sandbox_boundary", boundary).orEmpty().ifBlank { boundary }
        val approval = value.optJSONObject("approval_policy")
        approvalMode = approval?.optString("mode", approvalMode).orEmpty().ifBlank { approvalMode }
        reviewer = value.optString("approval_reviewer", reviewer).ifBlank { reviewer }
        approval?.optJSONObject("granular")?.let { granular ->
            fileWrites = granular.optBoolean("file_writes", fileWrites)
            commands = granular.optBoolean("commands", commands)
            networkApproval = granular.optBoolean("network", networkApproval)
            mcp = granular.optBoolean("mcp", mcp)
            management = granular.optBoolean("management", management)
            other = granular.optBoolean("other", other)
        }
        inheritSettings = scope == "workspace" && current.optString("settings_scope") != "workspace"
        confirmFull = false
    }

    val workspaces = state.snapshot.workspaces
    val conversations = state.snapshot.conversations
    Column(modifier.verticalScroll(rememberScrollState()), verticalArrangement = Arrangement.spacedBy(10.dp)) {
        Text("权限与审批策略", style = MaterialTheme.typography.headlineSmall)
        Text("此页面只更新 AgentDock 工具准入策略；不会获取 Android、Termux 或操作系统权限。")
        ResourceSection("作用域") {
            ResourceActions {
                listOf("global" to "全局", "workspace" to "工作区", "conversation" to "对话模式").forEach { (value, label) ->
                    FilterChip(scope == value, {
                        scope = value
                        scopeId = ""
                        confirmFull = false
                        inheritSettings = false
                    }, label = { Text(label) })
                }
            }
            if (scope != "global") {
                ResourceField(if (scope == "workspace") "工作区 ID" else "对话 ID", scopeId, { scopeId = it.take(128) })
                Row(Modifier.fillMaxWidth().horizontalScroll(rememberScrollState()), horizontalArrangement = Arrangement.spacedBy(6.dp)) {
                    val candidates = if (scope == "workspace") workspaces else conversations
                    candidates.take(100).forEach { item ->
                        FilterChip(scopeId == item.id, { scopeId = item.id }, label = { Text(item.title.take(32)) })
                    }
                }
            }
            ResourceActions {
                Button(onClick = ::reload, enabled = validSelection()) { Text("读取有效策略") }
                Text("revision：${if (revision > 0) revision else "未读取"}", Modifier.padding(top = 12.dp))
            }
            ResourceFeedback(view, ::reload)
        }

        if (effective != null) ResourceSection("Core 回读") {
            ResourceObject(effective, listOf("mode", "scope", "scope_id", "revision", "settings_source", "settings_scope", "settings_scope_id", "custom_permissions_enabled", "custom_permissions_scope", "custom_permissions_scope_id"))
            effective.optJSONObject("settings")?.let { PermissionSettingsReadback("当前有效值", it) }
            effective.optJSONObject("configured_settings")?.let { PermissionSettingsReadback("已配置值", it) }
            Text("操作系统权限未改变：${envelope?.optBoolean("os_privileges_unchanged", true)}")
            Text("执行边界：${envelope?.optString("sandbox_enforcement", "tool_admission_only")}")
        }

        ResourceSection("模式") {
            ResourceActions {
                val choices = if (scope == "conversation") listOf("readonly" to "只读", "rules" to "规则")
                else listOf("readonly" to "只读", "rules" to "规则", "full" to "完全权限")
                choices.forEach { (value, label) -> FilterChip(mode == value, { mode = value; confirmFull = false }, label = { Text(label) }) }
            }
            if (mode == "full") Row {
                Checkbox(confirmFull, { confirmFull = it })
                Text("确认在此作用域启用完全权限；显式 deny 规则仍生效", Modifier.padding(top = 12.dp))
            }
            if (scope == "conversation") Text("对话作用域只允许 readonly/rules；对话 ID 不授予 Full。")
        }

        if (scope != "conversation") ResourceSection("自定义权限设置") {
            if (scope == "workspace") Row {
                Checkbox(inheritSettings, { inheritSettings = it })
                Text("继承全局设置并删除此工作区的本地覆盖", Modifier.padding(top = 12.dp))
            }
            if (!inheritSettings) {
                Row(Modifier.fillMaxWidth(), horizontalArrangement = Arrangement.SpaceBetween) {
                    Column(Modifier.weight(1f)) {
                        Text("启用自定义 Permission Profile")
                        Text("关闭时保留已配置值，由执行模式决定实际设置。")
                    }
                    Switch(customEnabled, { customEnabled = it })
                }
                PermissionChoice("文件系统", listOf("deny" to "拒绝", "read" to "只读", "write" to "读写"), filesystem) { filesystem = it }
                PermissionChoice("网络", listOf("deny" to "拒绝", "allow" to "允许"), network) { network = it }
                PermissionChoice("工作区边界", listOf("none" to "无", "workspace" to "限制到工作区"), boundary) { boundary = it }
                PermissionChoice("审批策略", listOf("on-request" to "按需", "never" to "拒绝需审批操作", "granular" to "分类"), approvalMode) { approvalMode = it }
                PermissionChoice("审批者", listOf("user" to "用户", "auto_review" to "自动审查"), reviewer) { reviewer = it }
                if (approvalMode == "granular") {
                    PermissionToggle("文件写入", fileWrites) { fileWrites = it }
                    PermissionToggle("命令", commands) { commands = it }
                    PermissionToggle("网络", networkApproval) { networkApproval = it }
                    PermissionToggle("MCP", mcp) { mcp = it }
                    PermissionToggle("管理操作", management) { management = it }
                    PermissionToggle("其他", other) { other = it }
                }
            }
        }

        ResourceActions {
            Button(onClick = {
                prompt.ask({
                    WorkbenchCommands.permission(PermissionDraft(
                        scope, scopeId, revision, mode, confirmFull, customEnabled, inheritSettings,
                        filesystem, network, boundary, approvalMode, reviewer,
                        fileWrites, commands, networkApproval, mcp, management, other
                    ))
                }) { reload() }
            }, enabled = !resources.actionBusy && revision > 0 && validSelection() && (mode != "full" || confirmFull)) { Text("保存并回读") }
            OutlinedButton(onClick = ::reload, enabled = validSelection()) { Text("放弃本地编辑") }
        }
        ResourceReceipt(resources.actionResult)
    }
}

@Composable
fun PermissionChoice(title: String, values: List<Pair<String, String>>, selected: String, change: (String) -> Unit) {
    Text(title, style = MaterialTheme.typography.titleSmall)
    ResourceActions { values.forEach { (value, label) -> FilterChip(selected == value, { change(value) }, label = { Text(label) }) } }
}

@Composable
private fun PermissionToggle(title: String, checked: Boolean, change: (Boolean) -> Unit) {
    Row(Modifier.fillMaxWidth(), horizontalArrangement = Arrangement.SpaceBetween) {
        Text(title, Modifier.padding(top = 12.dp))
        Switch(checked, change)
    }
}

@Composable
private fun PermissionSettingsReadback(title: String, value: JSONObject) {
    Text(title, style = MaterialTheme.typography.titleSmall)
    value.optJSONObject("permission_profile")?.let { ResourceObject(it, listOf("filesystem", "network", "sandbox_boundary")) }
    value.optJSONObject("approval_policy")?.let {
        ResourceObject(it, listOf("mode"))
        it.optJSONObject("granular")?.let { granular -> ResourceObject(granular, listOf("file_writes", "commands", "network", "mcp", "management", "other")) }
    }
    value.optString("approval_reviewer").takeIf(String::isNotBlank)?.let { Text("approval_reviewer：$it") }
}
