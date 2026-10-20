package dev.agentdock.workbench.ui

import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.lazy.items
import androidx.compose.material3.Button
import androidx.compose.material3.Card
import androidx.compose.material3.Checkbox
import androidx.compose.material3.FilterChip
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.OutlinedButton
import androidx.compose.material3.OutlinedTextField
import androidx.compose.material3.Text
import androidx.compose.material3.TextButton
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.saveable.rememberSaveable
import androidx.compose.runtime.setValue
import androidx.compose.ui.Modifier
import androidx.compose.ui.text.input.PasswordVisualTransformation
import androidx.compose.ui.unit.dp
import androidx.lifecycle.compose.collectAsStateWithLifecycle
import dev.agentdock.workbench.data.CorePages
import dev.agentdock.workbench.data.ManagementContract
import dev.agentdock.workbench.data.WorkbenchCommands
import org.json.JSONObject

@Composable
fun PluginMcpManagementPage(state: WorkbenchUiState, model: WorkbenchViewModel, modifier: Modifier) {
    val resources by model.resources.state.collectAsStateWithLifecycle()
    val prompt = rememberCommandPrompt(model)
    var section by rememberSaveable { mutableStateOf("plugins") }
    var pluginSource by rememberSaveable { mutableStateOf("") }
    var pluginReview by remember { mutableStateOf<JSONObject?>(null) }
    var pluginReviewToken by remember { mutableStateOf("") }
    var reviewedPluginSource by remember { mutableStateOf("") }
    var reviewedPluginName by remember { mutableStateOf("") }
    var confirmedSourceChange by rememberSaveable { mutableStateOf(false) }
    var selectedPlugin by rememberSaveable { mutableStateOf("") }
    var memberType by rememberSaveable { mutableStateOf("skill") }
    var memberName by rememberSaveable { mutableStateOf("") }
    var selectedMcp by rememberSaveable { mutableStateOf("") }
    var mcpName by rememberSaveable { mutableStateOf("") }
    var transport by rememberSaveable { mutableStateOf("streamable_http") }
    var url by rememberSaveable { mutableStateOf("") }
    var executable by rememberSaveable { mutableStateOf("") }
    var arguments by rememberSaveable { mutableStateOf("") }
    var cwd by rememberSaveable { mutableStateOf("") }
    var timeout by rememberSaveable { mutableStateOf("30000") }
    var envKey by rememberSaveable(selectedMcp) { mutableStateOf("") }
    var envValue by remember(selectedMcp) { mutableStateOf("") }

    fun clearPluginReview() {
        pluginReview = null
        pluginReviewToken = ""
        reviewedPluginSource = ""
        reviewedPluginName = ""
        confirmedSourceChange = false
    }
    fun acceptPluginReview(result: JSONObject, source: String) {
        val review = result.optJSONObject("review")
        if (review == null) {
            clearPluginReview()
            model.showNotice("Core 未返回插件审查结果。")
            return
        }
        val token = review.optString("review_token")
        val valid = result.optBoolean("valid") && review.optBoolean("valid", true)
        if (!valid || token.isBlank()) {
            clearPluginReview()
            model.showNotice("插件来源未通过验证，或 Core 未返回安装授权令牌。")
            return
        }
        pluginReview = JSONObject(review.toString())
        pluginReviewToken = token
        reviewedPluginSource = source
        reviewedPluginName = review.optString("name")
        confirmedSourceChange = false
        model.showNotice("来源验证通过；安装或更新将使用本次验证令牌。")
    }
    fun reloadPlugins() = model.resources.read("plugins", "/internal/runtime/plugins")
    fun reloadMcp() = model.resources.read("mcp", "/internal/runtime/mcp")
    fun selectPlugin(name: String) {
        selectedPlugin = name
        model.resources.read("plugin-detail", "/internal/runtime/plugins/${ManagementContract.namedSegment(name)}")
    }
    fun selectMcp(name: String) {
        selectedMcp = name
        envValue = ""
        model.resources.read("mcp-detail", "/internal/runtime/mcp/${ManagementContract.namedSegment(name)}")
    }
    LaunchedEffect(state.settings.endpoint, state.settings.remoteEndpointEnabled) {
        clearPluginReview()
        reloadPlugins()
        reloadMcp()
    }

    val pluginsView = resources.views["plugins"]
    val plugins = remember(pluginsView?.data) { runCatching { pluginsView?.data?.let { CorePages.rows(it, "plugins") }.orEmpty() } }
    val mcpView = resources.views["mcp"]
    val servers = remember(mcpView?.data) { runCatching { mcpView?.data?.let { CorePages.rows(it, "servers") }.orEmpty() } }
    val pluginDetail = resources.views["plugin-detail"]?.data
    val mcpDetail = resources.views["mcp-detail"]?.data

    LazyColumn(modifier, verticalArrangement = Arrangement.spacedBy(10.dp)) {
        item {
            Text("插件与 MCP", style = MaterialTheme.typography.headlineSmall)
            Text("安装、更新、移除与环境修改均冻结明确对象并显示影响；库存与终态从 Core 回读。")
            ResourceActions {
                FilterChip(section == "plugins", { section = "plugins" }, label = { Text("插件") })
                FilterChip(section == "mcp", { section = "mcp" }, label = { Text("MCP") })
            }
        }
        if (section == "plugins") {
            item {
                ResourceSection("来源验证与安装") {
                    ResourceField("插件目录、归档或受支持来源", pluginSource, {
                        val next = it.take(4096)
                        if (next != pluginSource) clearPluginReview()
                        pluginSource = next
                    }, multiline = true)
                    ResourceActions {
                        OutlinedButton(onClick = {
                            val candidateSource = pluginSource
                            prompt.ask({ WorkbenchCommands.plugin("validate", source = candidateSource) }) { result ->
                                acceptPluginReview(result, candidateSource)
                            }
                        }, enabled = !resources.actionBusy && pluginSource.isNotBlank()) { Text("验证来源") }
                        Button(onClick = {
                            prompt.ask({
                                WorkbenchCommands.plugin(
                                    "install",
                                    source = pluginSource,
                                    reviewToken = pluginReviewToken
                                )
                            }) {
                                clearPluginReview()
                                reloadPlugins()
                            }
                        }, enabled = !resources.actionBusy && pluginReviewToken.isNotBlank() && reviewedPluginSource == pluginSource) { Text("安装已验证来源") }
                        TextButton(onClick = ::reloadPlugins) { Text("刷新库存") }
                    }
                    pluginReview?.let { review ->
                        ResourceObject(review, listOf("valid", "name", "version", "description", "package_digest"))
                        review.optJSONArray("warnings")?.let { warnings ->
                            for (index in 0 until minOf(warnings.length(), 20)) Text(warnings.optString(index).take(2048))
                        }
                        review.optJSONArray("issues")?.let { issues ->
                            for (index in 0 until minOf(issues.length(), 20)) Text(issues.optString(index).take(2048))
                        }
                    }
                    Text("安装和更新必须使用当前来源最近一次验证返回的令牌；编辑来源会立即清除该令牌。")
                    ResourceFeedback(pluginsView, ::reloadPlugins)
                    plugins.exceptionOrNull()?.let { Text(it.message.orEmpty()) }
                }
            }
            items(plugins.getOrDefault(emptyList()), key = { it.optString("name") }) { row ->
                val name = row.optString("name")
                Card(Modifier.fillMaxWidth()) {
                    Row(Modifier.padding(12.dp), horizontalArrangement = Arrangement.spacedBy(8.dp)) {
                        Column(Modifier.weight(1f)) {
                            Text(name, style = MaterialTheme.typography.titleMedium)
                            ResourceObject(row, listOf("name", "version", "enabled", "heavy", "source", "source_type", "skill_count", "mcp_count", "warning"))
                        }
                        TextButton(onClick = { selectPlugin(name) }) { Text(if (selectedPlugin == name) "刷新详情" else "详情") }
                    }
                }
            }
            if (selectedPlugin.isNotBlank()) item {
                ResourceSection("插件详情 · $selectedPlugin") {
                    ResourceFeedback(resources.views["plugin-detail"]) { selectPlugin(selectedPlugin) }
                    pluginDetail?.let { value ->
                        ResourceObject(value.optJSONObject("plugin") ?: value, listOf("name", "version", "enabled", "heavy", "source", "source_type", "description", "revision"))
                        value.optJSONArray("skills")?.let { array -> for (index in 0 until minOf(array.length(), 500)) array.optJSONObject(index)?.let { ResourceObject(it, listOf("name", "enabled", "description")) } }
                        value.optJSONArray("mcp_servers")?.let { array -> for (index in 0 until minOf(array.length(), 500)) array.optJSONObject(index)?.let { ResourceObject(it, listOf("name", "enabled", "transport", "description")) } }
                    }
                    ResourceActions {
                        Button(onClick = { prompt.ask({ WorkbenchCommands.plugin("enable", selectedPlugin) }) { reloadPlugins(); selectPlugin(selectedPlugin) } }, enabled = !resources.actionBusy) { Text("启用") }
                        OutlinedButton(onClick = { prompt.ask({ WorkbenchCommands.plugin("disable", selectedPlugin) }) { reloadPlugins(); selectPlugin(selectedPlugin) } }, enabled = !resources.actionBusy) { Text("停用") }
                        OutlinedButton(onClick = { prompt.ask({ WorkbenchCommands.plugin("heavy_enable", selectedPlugin) }) { reloadPlugins(); selectPlugin(selectedPlugin) } }, enabled = !resources.actionBusy) { Text("启用 Heavy") }
                        OutlinedButton(onClick = { prompt.ask({ WorkbenchCommands.plugin("heavy_disable", selectedPlugin) }) { reloadPlugins(); selectPlugin(selectedPlugin) } }, enabled = !resources.actionBusy) { Text("停用 Heavy") }
                    }
                    Row {
                        Checkbox(confirmedSourceChange, { confirmedSourceChange = it })
                        Text("确认在 Core 判定来源变化时重新绑定插件来源", Modifier.padding(top = 12.dp))
                    }
                    ResourceActions {
                        Button(onClick = {
                            prompt.ask({
                                WorkbenchCommands.plugin(
                                    "update",
                                    selectedPlugin,
                                    source = pluginSource,
                                    reviewToken = pluginReviewToken,
                                    confirmedSourceChange = confirmedSourceChange
                                )
                            }) {
                                clearPluginReview()
                                reloadPlugins()
                                selectPlugin(selectedPlugin)
                            }
                        }, enabled = !resources.actionBusy && pluginReviewToken.isNotBlank() && reviewedPluginSource == pluginSource && reviewedPluginName == selectedPlugin) { Text("更新为已验证来源") }
                        OutlinedButton(onClick = { prompt.ask({ WorkbenchCommands.plugin("remove", selectedPlugin) }) { selectedPlugin = ""; clearPluginReview(); reloadPlugins(); reloadMcp() } }, enabled = !resources.actionBusy) { Text("移除插件") }
                    }
                    PermissionChoice("成员类型", listOf("skill" to "Skill", "mcp_server" to "MCP 服务"), memberType) { memberType = it }
                    ResourceField("成员名称", memberName, { memberName = it.take(256) })
                    ResourceActions {
                        OutlinedButton(onClick = { prompt.ask({ WorkbenchCommands.plugin("member_enable", selectedPlugin, memberType = memberType, member = memberName) }) { selectPlugin(selectedPlugin); reloadMcp() } }, enabled = !resources.actionBusy && memberName.isNotBlank()) { Text("启用成员") }
                        OutlinedButton(onClick = { prompt.ask({ WorkbenchCommands.plugin("member_disable", selectedPlugin, memberType = memberType, member = memberName) }) { selectPlugin(selectedPlugin); reloadMcp() } }, enabled = !resources.actionBusy && memberName.isNotBlank()) { Text("停用成员") }
                    }
                }
            }
        } else {
            item {
                ResourceSection("新增 MCP") {
                    ResourceField("名称", mcpName, { mcpName = it.take(128) })
                    PermissionChoice("传输", listOf("streamable_http" to "HTTP", "stdio" to "stdio"), transport) { transport = it }
                    if (transport == "streamable_http") ResourceField("URL", url, { url = it.take(4096) })
                    else {
                        ResourceField("可执行文件", executable, { executable = it.take(4096) })
                        ResourceField("参数（每行一项）", arguments, { arguments = it.take(16_384) }, multiline = true)
                        ResourceField("工作目录", cwd, { cwd = it.take(4096) })
                    }
                    ResourceField("超时（毫秒）", timeout, { timeout = it.filter(Char::isDigit).take(6) })
                    Button(onClick = {
                        prompt.ask({ WorkbenchCommands.mcp("add", mcpName, transport, url, executable, arguments.lines().filter(String::isNotBlank), cwd, timeout.toIntOrNull() ?: 30000) }) { reloadMcp() }
                    }, enabled = !resources.actionBusy && mcpName.isNotBlank() && (url.isNotBlank() || executable.isNotBlank())) { Text("新增 MCP") }
                    TextButton(onClick = ::reloadMcp) { Text("刷新 MCP") }
                    ResourceFeedback(mcpView, ::reloadMcp)
                    servers.exceptionOrNull()?.let { Text(it.message.orEmpty()) }
                }
            }
            items(servers.getOrDefault(emptyList()), key = { it.optString("name") }) { row ->
                val name = row.optString("name")
                Card(Modifier.fillMaxWidth()) {
                    Row(Modifier.padding(12.dp), horizontalArrangement = Arrangement.spacedBy(8.dp)) {
                        Column(Modifier.weight(1f)) {
                            Text(name, style = MaterialTheme.typography.titleMedium)
                            ResourceObject(row, listOf("name", "description", "enabled", "connected", "transport", "url", "command", "timeout_ms", "plugin", "tool_count", "revision", "error"))
                        }
                        TextButton(onClick = { selectMcp(name) }) { Text(if (selectedMcp == name) "刷新详情" else "详情") }
                    }
                }
            }
            if (selectedMcp.isNotBlank()) item {
                ResourceSection("MCP 详情 · $selectedMcp") {
                    ResourceFeedback(resources.views["mcp-detail"]) { selectMcp(selectedMcp) }
                    mcpDetail?.let { value ->
                        ResourceObject(value.optJSONObject("server") ?: value, listOf("name", "description", "enabled", "connected", "transport", "url", "command", "cwd", "timeout_ms", "plugin", "tool_count", "revision", "error"))
                        value.optJSONObject("config")?.let { ResourceObject(it, listOf("name", "description", "transport", "url", "command", "cwd", "timeout_ms", "enabled", "source_type", "plugin_name")) }
                    }
                    ResourceActions {
                        Button(onClick = { prompt.ask({ WorkbenchCommands.mcp("enable", selectedMcp) }) { reloadMcp(); selectMcp(selectedMcp) } }, enabled = !resources.actionBusy) { Text("启用") }
                        OutlinedButton(onClick = { prompt.ask({ WorkbenchCommands.mcp("disable", selectedMcp) }) { reloadMcp(); selectMcp(selectedMcp) } }, enabled = !resources.actionBusy) { Text("停用") }
                        OutlinedButton(onClick = { prompt.ask({ WorkbenchCommands.mcp("refresh", selectedMcp) }) { reloadMcp(); selectMcp(selectedMcp) } }, enabled = !resources.actionBusy) { Text("刷新工具") }
                        OutlinedButton(onClick = { prompt.ask({ WorkbenchCommands.mcp("remove", selectedMcp) }) { selectedMcp = ""; reloadMcp() } }, enabled = !resources.actionBusy) { Text("移除") }
                    }
                    ResourceField("环境变量名", envKey, { envKey = it.take(128) })
                    OutlinedTextField(envValue, { envValue = it.take(32_768) }, label = { Text("环境变量值（不保存到页面状态）") }, modifier = Modifier.fillMaxWidth(), singleLine = true, visualTransformation = PasswordVisualTransformation())
                    ResourceActions {
                        Button(onClick = {
                            val frozen = envValue
                            prompt.ask({ WorkbenchCommands.mcpEnvironment(selectedMcp, envKey, frozen) }) { envValue = "" }
                        }, enabled = !resources.actionBusy && envKey.isNotBlank() && envValue.isNotEmpty()) { Text("保存隔离变量") }
                        OutlinedButton(onClick = { prompt.ask({ WorkbenchCommands.mcpEnvironment(selectedMcp, envKey, null) }) }, enabled = !resources.actionBusy && envKey.isNotBlank()) { Text("删除变量") }
                        TextButton(onClick = { prompt.ask({ WorkbenchCommands.mcp("env_list", selectedMcp) }) }) { Text("列出变量名") }
                    }
                    Text("变量值不进入 SavedState、DataStore、日志或详情回读；Core 只返回变量名及是否已配置。")
                }
            }
        }
        item { ResourceReceipt(resources.actionResult) }
    }
}
