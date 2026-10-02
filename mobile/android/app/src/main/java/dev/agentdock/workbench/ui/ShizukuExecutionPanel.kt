package dev.agentdock.workbench.ui

import android.content.Intent
import android.provider.Settings
import androidx.compose.material3.OutlinedButton
import androidx.compose.material3.Switch
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.DisposableEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.rememberCoroutineScope
import androidx.compose.runtime.setValue
import androidx.compose.ui.platform.LocalContext
import androidx.lifecycle.Lifecycle
import androidx.lifecycle.LifecycleEventObserver
import androidx.lifecycle.compose.LocalLifecycleOwner
import androidx.lifecycle.compose.collectAsStateWithLifecycle
import dev.agentdock.workbench.WorkbenchApplication
import dev.agentdock.workbench.shizuku.ShizukuBackend
import kotlinx.coroutines.CancellationException
import kotlinx.coroutines.launch

@Composable
fun ShizukuExecutionPanel(fixture: Boolean) {
    if (fixture) return
    val context = LocalContext.current
    val executor = (context.applicationContext as WorkbenchApplication).graph.phoneExecutor
    val shizuku = executor.shizuku
    val status by shizuku.state.collectAsStateWithLifecycle()
    var enabled by remember { mutableStateOf(executor.backendEnabled(shizuku.name)) }
    var message by remember { mutableStateOf("") }
    var verifying by remember { mutableStateOf(false) }
    val scope = rememberCoroutineScope()
    val lifecycle = LocalLifecycleOwner.current.lifecycle
    DisposableEffect(lifecycle) {
        val observer = LifecycleEventObserver { _, event -> if (event == Lifecycle.Event.ON_RESUME) shizuku.refresh() }
        lifecycle.addObserver(observer); shizuku.refresh()
        onDispose { lifecycle.removeObserver(observer) }
    }
    ResourceSection("本机系统控制 · Shizuku") {
        Text("状态：${status.state}；服务 API：${status.serverVersion ?: "未知"}；实际 UID：${status.uid ?: "未验证"}")
        Text("无线调试配对与启动在 Shizuku 中完成。开启后由原 Core 权限与会话管理系统命令；关闭后不再接受新系统命令，并核对在途取消。")
        Switch(checked = enabled, onCheckedChange = { value -> executor.setBackendEnabled(shizuku.name, value); enabled = value })
        if (message.isNotBlank()) Text(message)
        ResourceActions {
            OutlinedButton(onClick = {
                val intent = context.packageManager.getLaunchIntentForPackage(ShizukuBackend.PACKAGE)
                message = if (intent == null) "未找到 Shizuku 启动入口，请核对安装状态。" else runCatching { context.startActivity(intent); "已打开 Shizuku" }.getOrDefault("无法打开 Shizuku")
            }) { Text("打开 Shizuku") }
            OutlinedButton(onClick = {
                message = runCatching { context.startActivity(Intent(Settings.ACTION_APPLICATION_DEVELOPMENT_SETTINGS)); "在开发者选项中打开无线调试。" }.getOrDefault("系统未提供开发者选项入口，请从系统设置进入。")
            }) { Text("开发者选项") }
            OutlinedButton(onClick = {
                message = runCatching { shizuku.requestPermission(); "请完成 Shizuku 原生授权。" }.getOrDefault("服务未运行或授权请求未发送，请核对 Shizuku 状态。")
            }) { Text("请求 Shizuku 授权") }
            OutlinedButton(enabled = !verifying, onClick = {
                verifying = true
                scope.launch {
                    try { message = executor.verify(shizuku.name).toString() }
                    catch (e: CancellationException) { throw e }
                    catch (_: Exception) { message = "验证未完成；核对服务版本、授权及连接状态。未重放系统命令。" }
                    finally { verifying = false }
                }
            }) { Text("验证系统通道") }
        }
    }
}
