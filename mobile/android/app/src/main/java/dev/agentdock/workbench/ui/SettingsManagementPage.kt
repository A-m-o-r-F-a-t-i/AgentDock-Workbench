package dev.agentdock.workbench.ui

import android.Manifest
import android.os.Build
import androidx.activity.compose.rememberLauncherForActivityResult
import androidx.activity.result.contract.ActivityResultContracts
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.verticalScroll
import androidx.compose.material3.Button
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Slider
import androidx.compose.material3.Switch
import androidx.compose.material3.Text
import androidx.compose.material3.TextButton
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableFloatStateOf
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.saveable.rememberSaveable
import androidx.compose.runtime.setValue
import androidx.compose.ui.Modifier
import androidx.compose.ui.unit.dp
import androidx.lifecycle.compose.collectAsStateWithLifecycle
import dev.agentdock.workbench.data.WorkbenchCommands

@Composable
fun SettingsManagementPage(state: WorkbenchUiState, model: WorkbenchViewModel, modifier: Modifier) {
    val resources by model.resources.state.collectAsStateWithLifecycle()
    val prompt = rememberCommandPrompt(model)
    var mcpUi by rememberSaveable { mutableStateOf(false) }
    var outputEnabled by rememberSaveable { mutableStateOf(true) }
    var outputLimit by rememberSaveable { mutableStateOf("20000") }
    var interval by remember(state.settings.guardianIntervalMinutes) { mutableFloatStateOf(state.settings.guardianIntervalMinutes.toFloat()) }

    fun reloadDisplay() = model.resources.read("display", "/internal/runtime/execution/display")
    LaunchedEffect(state.settings.endpoint, state.settings.remoteEndpointEnabled) { reloadDisplay() }
    val display = resources.views["display"]
    LaunchedEffect(display?.data?.toString()) {
        val value = display?.data ?: return@LaunchedEffect
        mcpUi = value.optBoolean("chatgpt_mcp_ui_enabled")
        value.optJSONObject("tool_output")?.let {
            outputEnabled = it.optBoolean("enabled", true)
            outputLimit = it.optInt("max_chars", 20_000).toString()
        }
    }

    val notificationPermission = rememberLauncherForActivityResult(ActivityResultContracts.RequestPermission()) { granted ->
        if (granted) model.setNotificationsEnabled(true)
        else { model.setNotificationsEnabled(false); model.showNotice("系统通知权限未授予，通知和守护已请求关闭。") }
    }

    Column(modifier.verticalScroll(rememberScrollState()), verticalArrangement = Arrangement.spacedBy(10.dp)) {
        Text("设置", style = MaterialTheme.typography.headlineSmall)
        Text("调整界面、通知和节点运行方式。")

        ResourceSection("外观") {
            PermissionChoice("主题", listOf("system" to "跟随系统", "light" to "浅色", "dark" to "深色"), state.settings.theme) {
                model.updateSettings { current -> current.copy(theme = it) }
            }
            PermissionChoice("信息密度", listOf("comfortable" to "舒适", "compact" to "紧凑"), state.settings.density) {
                model.updateSettings { current -> current.copy(density = it) }
            }
            SettingsToggle("显示详细调用", "显示更多执行信息，输出长度仍受节点设置限制。", state.settings.detailedCalls) {
                model.updateSettings { current -> current.copy(detailedCalls = it) }
            }
            Text("界面语言：简体中文")
        }

        ResourceSection("通知与节点守护") {
            SettingsToggle("启用通知", "关闭时同步关闭守护，但不会停止 Core。Android 13+ 还需系统通知权限。", state.settings.notificationsEnabled) { enabled ->
                if (enabled && Build.VERSION.SDK_INT >= 33) notificationPermission.launch(Manifest.permission.POST_NOTIFICATIONS)
                else model.setNotificationsEnabled(enabled)
            }
            SettingsToggle("启用节点守护", "定期检查节点状态；系统省电设置可能影响运行。", state.settings.guardianEnabled, model::setGuardianEnabled)
            SettingsToggle("暂停守护", "暂停检查和自动恢复，不停止节点。", state.settings.guardianPaused, model::setGuardianPaused)
            SettingsToggle("允许受限自动恢复", "仅恢复本应运行的节点，不安装或升级程序。", state.settings.autoRepairEnabled) {
                model.updateSettings { current -> current.copy(autoRepairEnabled = it) }
            }
            SettingsToggle("开机健康检查", "开机后延迟检查节点状态。", state.settings.bootHealthCheckEnabled) {
                model.updateSettings { current -> current.copy(bootHealthCheckEnabled = it) }
            }
            SettingsToggle("仅已验证 Wi-Fi", "仅在 Wi-Fi 已连接互联网时检查。", state.settings.onlyOnWifi) {
                model.updateSettings { current -> current.copy(onlyOnWifi = it) }
            }
            SettingsToggle("仅充电时", "等待设备充电或充满后检查。", state.settings.onlyWhileCharging) {
                model.updateSettings { current -> current.copy(onlyWhileCharging = it) }
            }
            Text("检查间隔：${interval.toInt()} 分钟（Android 周期任务最短 15 分钟）")
            Slider(
                value = interval,
                onValueChange = { interval = it.coerceIn(15f, 1440f) },
                onValueChangeFinished = { model.updateSettings { current -> current.copy(guardianIntervalMinutes = interval.toInt().coerceIn(15, 1440)) } },
                valueRange = 15f..1440f
            )
        }

        ResourceSection("节点输出") {
            ResourceFeedback(display, ::reloadDisplay)
            val value = display?.data
            if (value != null) {
                ResourceObject(value, listOf("schema_version", "revision", "chatgpt_mcp_ui_enabled", "tool_output_unit", "server_policy_applied", "host_adoption", "warning", "refresh_hint"))
                value.optJSONObject("tool_output")?.let { ResourceObject(it, listOf("enabled", "max_chars")) }
            }
            SettingsToggle("允许 ChatGPT MCP UI", "对后续展示生效，不删除历史卡片。", mcpUi) { mcpUi = it }
            SettingsToggle("记录并提供工具输出", "关闭后只保留调用记录，不提供新的输出正文。", outputEnabled) { outputEnabled = it }
            ResourceField("输出上限（1,000–100,000 字符）", outputLimit, { outputLimit = it.filter(Char::isDigit).take(6) })
            ResourceActions {
                Button(onClick = {
                    val revision = display?.data?.optLong("revision", -1) ?: -1
                    val limit = outputLimit.toIntOrNull() ?: -1
                    prompt.ask({ WorkbenchCommands.display(revision, outputEnabled, limit, mcpUi) }) {
                        model.updateSettings { current -> current.copy(toolOutputEnabled = outputEnabled, toolOutputMaxChars = limit) }
                        reloadDisplay()
                    }
                }, enabled = !resources.actionBusy && (display?.data?.optLong("revision", -1) ?: -1) > 0 &&
                    (outputLimit.toIntOrNull()?.let { it in 1_000..100_000 } == true)) { Text("保存设置") }
                TextButton(onClick = ::reloadDisplay) { Text("放弃本地编辑") }
            }
            TechnicalDetails("输出说明") { Text("上限按 Unicode 标量计数，由节点执行；手机预览使用同一上限。") }
        }

        ResourceSection("关于 AgentDock") {
            Text("AgentDock ${dev.agentdock.workbench.BuildConfig.PRODUCT_VERSION}")
            TechnicalDetails("构建信息") { Text(state.buildIdentity) }
        }
        ResourceReceipt(resources.actionResult)
    }
}

@Composable
private fun SettingsToggle(title: String, description: String, checked: Boolean, change: (Boolean) -> Unit) {
    Row(Modifier.fillMaxWidth(), horizontalArrangement = Arrangement.spacedBy(12.dp)) {
        Column(Modifier.weight(1f)) {
            Text(title, style = MaterialTheme.typography.titleSmall)
            Text(description, style = MaterialTheme.typography.bodySmall)
        }
        Switch(checked, change)
    }
}
