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
import androidx.compose.foundation.layout.weight
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
        else model.showNotice("系统通知权限未授予，通知和守护保持关闭。")
    }

    Column(modifier.verticalScroll(rememberScrollState()), verticalArrangement = Arrangement.spacedBy(10.dp)) {
        Text("设置", style = MaterialTheme.typography.headlineSmall)
        Text("本地界面设置、Android 生命周期设置和 Core 服务端设置分别保存，并在页面中标明实际作用域。")

        ResourceSection("本地界面") {
            PermissionChoice("主题", listOf("system" to "跟随系统", "light" to "浅色", "dark" to "深色"), state.settings.theme) {
                model.updateSettings { current -> current.copy(theme = it) }
            }
            PermissionChoice("信息密度", listOf("comfortable" to "舒适", "compact" to "紧凑"), state.settings.density) {
                model.updateSettings { current -> current.copy(density = it) }
            }
            SettingsToggle("显示详细调用", "展示 Core 返回的详细字段；工具输出仍受服务端策略和客户端上限约束。", state.settings.detailedCalls) {
                model.updateSettings { current -> current.copy(detailedCalls = it) }
            }
            Text("界面语言：简体中文。本候选尚未提供完整翻译资源，因此不显示无效的语言切换器。")
        }

        ResourceSection("通知与节点守护") {
            SettingsToggle("启用通知", "关闭时同步关闭守护，但不会停止 Core。Android 13+ 还需系统通知权限。", state.settings.notificationsEnabled) { enabled ->
                if (enabled && Build.VERSION.SDK_INT >= 33) notificationPermission.launch(Manifest.permission.POST_NOTIFICATIONS)
                else model.setNotificationsEnabled(enabled)
            }
            SettingsToggle("启用节点守护", "用户显式启动的前台服务和周期 WorkManager；系统仍可按电源策略停止它。", state.settings.guardianEnabled, model::setGuardianEnabled)
            SettingsToggle("暂停守护", "只暂停健康检查与受限恢复，不改变 Core desired state。", state.settings.guardianPaused, model::setGuardianPaused)
            SettingsToggle("允许受限自动恢复", "只在 desired=running 时重启当前已验证版本；不会下载安装或升级。", state.settings.autoRepairEnabled) {
                model.updateSettings { current -> current.copy(autoRepairEnabled = it) }
            }
            SettingsToggle("开机健康检查", "系统启动后由 WorkManager 延迟检查；广播不会直接启动 Core。", state.settings.bootHealthCheckEnabled) {
                model.updateSettings { current -> current.copy(bootHealthCheckEnabled = it) }
            }
            SettingsToggle("仅已验证 Wi-Fi", "前台守护和 WorkManager 都等待已验证的 Wi-Fi 连接。", state.settings.onlyOnWifi) {
                model.updateSettings { current -> current.copy(onlyOnWifi = it) }
            }
            SettingsToggle("仅充电时", "前台守护和 WorkManager 都等待设备处于充电或已充满状态。", state.settings.onlyWhileCharging) {
                model.updateSettings { current -> current.copy(onlyWhileCharging = it) }
            }
            Text("检查间隔：${interval.toInt()} 分钟（Android 周期任务最短 15 分钟）")
            Slider(
                value = interval,
                onValueChange = { interval = it.coerceIn(15f, 1440f) },
                onValueChangeFinished = { model.updateSettings { current -> current.copy(guardianIntervalMinutes = interval.toInt().coerceIn(15, 1440)) } },
                valueRange = 15f..1440f
            )
            Text("本页不承诺永久后台保活，不使用静音音频、频繁闹钟、WakeLock 循环、无障碍服务或厂商绕过。")
        }

        ResourceSection("Core 输出策略") {
            ResourceFeedback(display, ::reloadDisplay)
            val value = display?.data
            if (value != null) {
                ResourceObject(value, listOf("schema_version", "revision", "chatgpt_mcp_ui_enabled", "tool_output_unit", "server_policy_applied", "host_adoption", "warning", "refresh_hint"))
                value.optJSONObject("tool_output")?.let { ResourceObject(it, listOf("enabled", "max_chars")) }
            }
            SettingsToggle("允许 ChatGPT MCP UI", "改变 Core 后续模板/展示策略；已渲染的历史卡片不删除，宿主采纳状态仍由 Core 报告。", mcpUi) { mcpUi = it }
            SettingsToggle("记录并提供工具输出", "关闭后 Core 不提供后续工具输出正文；调用元数据仍保留。", outputEnabled) { outputEnabled = it }
            ResourceField("工具输出上限（Unicode 标量，1,000–100,000）", outputLimit, { outputLimit = it.filter(Char::isDigit).take(6) })
            ResourceActions {
                Button(onClick = {
                    val revision = display?.data?.optLong("revision", -1) ?: -1
                    val limit = outputLimit.toIntOrNull() ?: -1
                    prompt.ask({ WorkbenchCommands.display(revision, outputEnabled, limit, mcpUi) }) {
                        model.updateSettings { current -> current.copy(toolOutputEnabled = outputEnabled, toolOutputMaxChars = limit) }
                        reloadDisplay()
                    }
                }, enabled = !resources.actionBusy && (display?.data?.optLong("revision", -1) ?: -1) > 0 &&
                    (outputLimit.toIntOrNull()?.let { it in 1_000..100_000 } == true)) { Text("按 revision 保存") }
                TextButton(onClick = ::reloadDisplay) { Text("放弃本地编辑") }
            }
            Text("Android 只把同一上限用于分段预览；实际是否记录和最大输出量由 Core 策略决定。")
        }

        ResourceSection("明确未提供的开关") {
            Text("公网访问需要具体隧道提供方和共享控制接口；移动数据策略属于下载器/传输层。当前 APK 未实现这些能力，因此不显示不会生效的开关。")
            Text("候选构建：${state.buildIdentity}")
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
