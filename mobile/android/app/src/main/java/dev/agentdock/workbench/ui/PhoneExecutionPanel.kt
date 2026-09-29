package dev.agentdock.workbench.ui

import android.content.ClipData
import android.content.ClipboardManager
import dev.agentdock.workbench.BuildConfig
import dev.agentdock.workbench.execution.PhoneDiagnostics
import android.Manifest
import android.os.Build
import androidx.activity.compose.rememberLauncherForActivityResult
import androidx.activity.result.contract.ActivityResultContracts
import androidx.compose.material3.Button
import androidx.compose.material3.OutlinedButton
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.rememberCoroutineScope
import androidx.compose.runtime.setValue
import androidx.compose.ui.platform.LocalContext
import androidx.lifecycle.compose.collectAsStateWithLifecycle
import dev.agentdock.workbench.WorkbenchApplication
import dev.agentdock.workbench.termux.TermuxContract
import kotlinx.coroutines.CancellationException
import kotlinx.coroutines.launch

@Composable
fun PhoneExecutionPanel(state: WorkbenchUiState) {
    if (state.fixture) { ResourceSection("本机执行器") { Text("Fixture 不连接 Termux、不启动设备命令。") }; return }
    val app = LocalContext.current.applicationContext as WorkbenchApplication
    val executor = app.graph.phoneExecutor
    val status by executor.state.collectAsStateWithLifecycle()
    val scope = rememberCoroutineScope()
    var message by remember { mutableStateOf("") }
    var busy by remember { mutableStateOf(false) }
    val permission = rememberLauncherForActivityResult(ActivityResultContracts.RequestMultiplePermissions()) {
        message = "授权结果已返回，请运行端到端验证。"
    }
    ResourceSection("本机执行器 · Termux") {
        Text(status.message)
        if (status.origin.isNotBlank()) Text("固定绑定：${status.origin}")
        Text("工程默认后端保持不变。Termux 宿主命令由原 Core 会话管理；连接页面切换远程节点不会改变执行器目标。")
        if (message.isNotBlank()) Text(message)
        ResourceActions {
            OutlinedButton(onClick = {
                permission.launch(if (Build.VERSION.SDK_INT >= 33) arrayOf(TermuxContract.PERMISSION_RUN_COMMAND, Manifest.permission.POST_NOTIFICATIONS)
                    else arrayOf(TermuxContract.PERMISSION_RUN_COMMAND))
            }, enabled = !busy) { Text("请求宿主调用权限") }
            OutlinedButton(onClick = {
                busy = true
                scope.launch {
                    try { message = executor.verify("termux_host").toString() }
                    catch (error: CancellationException) { throw error }
                    catch (_: Exception) { message = "宿主验证未完成。核对已导出的新版桥、RUN_COMMAND 权限与外部调用开关；没有重装或重放命令。" }
                    finally { busy = false }
                }
            }, enabled = !busy) { Text("验证 Termux") }
            Button(onClick = {
                busy = true
                scope.launch {
                    try { executor.start(state.settings.endpoint) }
                    catch (error: CancellationException) { throw error }
                    catch (_: Exception) { message = "启动未完成，请核对本机 Core 配对及执行器状态。" }
                    finally { busy = false }
                }
            }, enabled = !busy && !status.enabled) { Text("启用本机执行器") }
            OutlinedButton(onClick = executor::stop, enabled = status.enabled) { Text("停止执行器") }
            OutlinedButton(onClick = {
                val report = PhoneDiagnostics.snapshot(BuildConfig.PRODUCT_VERSION, BuildConfig.CANDIDATE_SHA,
                    status.enabled, status.connected, executor.capabilities())
                app.getSystemService(ClipboardManager::class.java).setPrimaryClip(ClipData.newPlainText("AgentDock 手机连接诊断", report.toString(2)))
                message = "已复制连接状态与构建信息，不包含令牌、命令或环境变量。"
            }) { Text("复制连接诊断") }
        }
    }
    ShizukuExecutionPanel(state.fixture)
}
