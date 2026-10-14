package dev.agentdock.workbench.ui

import android.content.Intent
import android.net.Uri
import android.provider.Settings
import androidx.activity.compose.rememberLauncherForActivityResult
import androidx.activity.result.contract.ActivityResultContracts
import androidx.compose.foundation.horizontalScroll
import androidx.compose.foundation.layout.*
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.lazy.items
import androidx.compose.foundation.rememberScrollState
import androidx.compose.material3.*
import androidx.compose.runtime.*
import androidx.compose.runtime.saveable.rememberSaveable
import androidx.compose.ui.Modifier
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.platform.testTag
import androidx.compose.ui.unit.dp
import dev.agentdock.workbench.BuildConfig
import dev.agentdock.workbench.termux.TermuxContract
import org.json.JSONObject

private data class DeploymentConfirmation(val title: String, val explanation: String, val operation: String, val arguments: JSONObject)

/** Frontend of the fixed external-Termux protocol; no process or install state is invented here. */
@Composable
fun DeploymentPage(state: WorkbenchUiState, model: WorkbenchViewModel, modifier: Modifier) {
    val context = LocalContext.current
    val export = rememberLauncherForActivityResult(ActivityResultContracts.CreateDocument("application/zip")) { uri ->
        uri?.let(model::exportBridgeBundle)
    }
    val permission = rememberLauncherForActivityResult(ActivityResultContracts.RequestPermission()) { granted ->
        model.showNotice(if (granted) "系统已授予 RUN_COMMAND；仍需探测外部桥。" else "RUN_COMMAND 未获授权，未提交 Termux 操作。")
    }
    var startAfter by rememberSaveable { mutableStateOf(true) }
    var nodeName by rememberSaveable { mutableStateOf("AgentDock Workbench") }
    var port by rememberSaveable { mutableStateOf("8765") }
    var distro by rememberSaveable { mutableStateOf("debian") }
    var baseDelay by rememberSaveable { mutableStateOf("30") }
    var maxDelay by rememberSaveable { mutableStateOf("900") }
    var failures by rememberSaveable { mutableStateOf("3") }
    var workspaceRoot by rememberSaveable { mutableStateOf("/data/data/com.termux/files/home/storage/shared/Documents/AgentDock/Workspaces") }
    var adoptedPath by rememberSaveable { mutableStateOf("") }
    var targetOperation by rememberSaveable { mutableStateOf("") }
    var confirmation by remember { mutableStateOf<DeploymentConfirmation?>(null) }
    val status = state.operations.firstOrNull { it.operation in setOf("probe", "status") && it.resultJson.isNotBlank() }
        ?.let { runCatching { JSONObject(it.resultJson) }.getOrNull() }
    val cleanup = state.operations.firstOrNull { it.operation == "cleanup_preview" && it.phase == "succeeded" }
        ?.let { runCatching { JSONObject(it.resultJson) }.getOrNull() }
    val migration = state.operations.firstOrNull { it.operation == "workspace_migration_preview" && it.phase == "succeeded" }
        ?.let { runCatching { JSONObject(it.resultJson) }.getOrNull() }
    val uninstall = state.operations.firstOrNull { it.operation == "uninstall_preview" && it.phase == "succeeded" }
        ?.let { runCatching { JSONObject(it.resultJson) }.getOrNull() }
    val enabled = !state.actionBusy
    fun offer(title: String, message: String, operation: String, arguments: JSONObject = JSONObject()) {
        confirmation = DeploymentConfirmation(title, message, operation, JSONObject(arguments.toString()))
    }
    confirmation?.let { frozen ->
        AlertDialog(
            onDismissRequest = { confirmation = null },
            title = { Text(frozen.title) }, text = { Text(frozen.explanation) },
            confirmButton = { TextButton(onClick = {
                confirmation = null
                model.runTermux(frozen.operation, frozen.arguments)
            }, modifier = Modifier.testTag("confirm-deployment")) { Text("确认执行") } },
            dismissButton = { TextButton(onClick = { confirmation = null }) { Text("取消") } }
        )
    }
    LazyColumn(modifier, verticalArrangement = Arrangement.spacedBy(12.dp)) {
        item {
            Text("安装与更新", style = MaterialTheme.typography.headlineSmall)
            Text("准备运行环境，安装或更新节点。")
            Text("首次使用需要安装 Termux，并完成下面的环境准备。")
        }
        item { TermuxRecoveryPanel(state, model) }
        item {
            DeploymentSection("准备运行环境") {
                DeploymentRow {
                    OutlinedButton(onClick = { export.launch("AgentDock-Workbench-Termux-bridge.zip") }, enabled = enabled) { Text("导出安装脚本") }
                    OutlinedButton(onClick = { permission.launch(TermuxContract.PERMISSION_RUN_COMMAND) }) { Text("授予 RUN_COMMAND") }
                }
                Text("在 Termux 中解压导出包，保留所有文件并执行 bootstrap 脚本。完成后点击检测。")
                DeploymentRow {
                    TextButton(onClick = {
                        val launch = context.packageManager.getLaunchIntentForPackage(TermuxContract.PACKAGE)
                        if (launch == null) model.showNotice("未发现官方 Termux，请先安装外部 Termux。")
                        else runCatching { context.startActivity(launch) }.onFailure { model.showNotice("无法打开 Termux，请从系统应用列表进入。") }
                    }) { Text("打开 Termux") }
                    TextButton(onClick = {
                        runCatching { context.startActivity(Intent(Settings.ACTION_APPLICATION_DETAILS_SETTINGS, Uri.parse("package:${context.packageName}"))) }
                            .onFailure { model.showNotice("系统未提供应用设置入口。") }
                    }) { Text("系统应用设置") }
                }
                DeploymentRow {
                    OutlinedButton(onClick = { model.runTermux("probe") }, enabled = enabled, modifier = Modifier.testTag("termux-probe")) { Text("检测环境") }
                    OutlinedButton(onClick = { model.runTermux("status") }, enabled = enabled) { Text("查询节点") }
                    OutlinedButton(onClick = { offer("初始化受管节点", "只准备新的私有受管目录和身份。不会下载安装 Core，也不会替换已有有效身份。", "bootstrap") }, enabled = enabled) { Text("初始化目录") }
                }
                if (status != null) {
                    val installedVersion = status.optString("version")
                    Text("Core 版本：${installedVersion.ifBlank { "未安装" }}")
                    Text("运行意图：${status.optString("desired_state", "未知")} · 身份：${status.optString("process_identity", "未知")}")
                    Text("已验证健康：${status.optBoolean("healthy")} · APK 内置 Core 已准备：${status.optBoolean("bundled_core_ready")}")
                    status.optJSONObject("workspace")?.let { workspace ->
                        Text("公共工作区：${workspace.optString("path", "未配置")}")
                        Text("用户可访问：${workspace.optBoolean("user_accessible")} · 当前可用：${workspace.optBoolean("available")}")
                        if (workspace.optBoolean("legacy_private_pending")) Text("检测到旧私有工作区，请在下方预览并迁移。")
                    }
                    if (installedVersion.isBlank()) {
                        Text("当前手机尚未安装受管 Core。运行包含 Core 的完整桥包后，使用下方按钮部署并启动，任务、活动与调用页面即可读取本机数据。")
                    }
                    val active = status.optString("active_operation_id")
                    if (active.isNotBlank()) TextButton(onClick = { targetOperation = active }) { Text("查看未完成事务 $active") }
                }
            }
        }
        item {
            DeploymentSection("节点配置") {
                DeploymentInput("节点名称", nodeName) { nodeName = it.take(80) }
                DeploymentInput("本机端口（1024–65535）", port) { port = it.take(5) }
                TechnicalDetails("高级配置") {
                    DeploymentInput("PRoot 发行版", distro) { distro = it.take(32) }
                    DeploymentInput("共享工作区（Termux 绝对路径）", workspaceRoot) { workspaceRoot = it.take(2048) }
                    Text("工作区必须位于 Termux 共享存储，可由系统文件管理器直接访问。运行数据、对话索引、凭据和日志继续保留在私有目录。")
                    DeploymentInput("首次恢复等待（秒）", baseDelay) { baseDelay = it.take(4) }
                    DeploymentInput("最长恢复等待（秒）", maxDelay) { maxDelay = it.take(4) }
                    DeploymentInput("连续恢复失败上限", failures) { failures = it.take(2) }
                }
                Button(onClick = {
                    val number = port.toIntOrNull(); val base = baseDelay.toIntOrNull(); val maximum = maxDelay.toIntOrNull(); val count = failures.toIntOrNull()
                    val sharedWorkspace = workspaceRoot.startsWith("/storage/emulated/0/") ||
                        workspaceRoot.startsWith("/data/data/com.termux/files/home/storage/shared/")
                    if (number !in 1024..65535 || base !in 15..300 || maximum == null || base == null || maximum !in base..3600 || count !in 1..10 || !sharedWorkspace ||
                        !Regex("[a-z][a-z0-9_-]{0,31}").matches(distro) || nodeName.isBlank()) {
                        model.showNotice("请检查端口、发行版、共享工作区与恢复策略范围。")
                    } else {
                        val node = JSONObject().put("port", number).put("distro", distro).put("node_name", nodeName)
                            .put("workspace_root", workspaceRoot).put("recovery_base_seconds", base)
                            .put("recovery_max_seconds", maximum).put("recovery_max_failures", count)
                        offer("保存节点配置", "仅在受管节点已停止时写入。工作区只允许共享存储路径；改变工作区不会自动合并现有文件。连接页面的 Origin 不会被隐式改写。", "configure", JSONObject().put("node", node))
                    }
                }, enabled = enabled) { Text("保存停止节点配置") }
                DeploymentRow {
                    OutlinedButton(onClick = { offer("启动 Core", "启动当前受管版本并验证身份、版本及管理接口。未知进程或端口冲突将保留待处理状态。", "start") }, enabled = enabled) { Text("启动") }
                    OutlinedButton(onClick = { offer("停止 Core", "先持久保存停止意图，再停止已验证的受管进程。守护启用和暂停开关保持不变。", "stop") }, enabled = enabled) { Text("停止") }
                    OutlinedButton(onClick = { offer("重启 Core", "只重启当前受管版本，不下载安装。未决部署事务会阻止该操作。", "restart") }, enabled = enabled) { Text("重启") }
                    OutlinedButton(onClick = { offer("恢复并重置熔断", "明确重置连续失败计数。只有节点运行意图为 running 时才启动当前版本。", "repair", JSONObject().put("confirm_reset", true)) }, enabled = enabled) { Text("恢复运行") }
                }
            }
        }
        item {
            DeploymentSection("安装与升级") {
                Text("AgentDock Workbench APK 已包含同版本 ARM64 Core。导出的完整桥包会把该 Core 写入 Termux 私有目录，安装与升级不再访问额外发布文件。")
                Row {
                    Checkbox(startAfter, { startAfter = it })
                    Text("明确允许启动并完成新版本健康验证", Modifier.padding(top = 12.dp))
                }
                DeploymentRow {
                    listOf("install" to "安装", "update" to "升级").forEach { (operation, title) ->
                        Button(onClick = {
                            offer(title, "部署 APK ${BuildConfig.PRODUCT_VERSION} 内置 Core。解包并核对 Core 自报版本后进入维护阶段，备份数据并切换版本。未允许启动时，事务会停留在待健康验证阶段。", operation,
                                JSONObject().put("start_after_install", startAfter))
                        }, enabled = enabled) { Text(title) }
                    }
                    OutlinedButton(onClick = { offer("回退版本与数据", "恢复上一份已验证健康版本及其一致性 Core 数据快照。当前数据会先保留在私有隔离目录；用户工程不变。", "rollback",
                        JSONObject().put("confirm_data_restore", true).put("start_after_install", startAfter)) }, enabled = enabled) { Text("回退") }
                }
            }
        }
        item {
            DeploymentSection("原操作恢复") {
                DeploymentInput("原操作编号", targetOperation) { targetOperation = it.take(96) }
                DeploymentRow {
                    OutlinedButton(onClick = { model.runTermux("operation_query", JSONObject().put("target_operation_id", targetOperation)) }, enabled = enabled && targetOperation.isNotBlank()) { Text("查询原操作") }
                    OutlinedButton(onClick = { offer("继续原部署事务", "沿原操作记录恢复，不创建第二次安装。不会重放缺失回执的普通运行命令。", "resume", JSONObject().put("target_operation_id", targetOperation).put("confirm_start", startAfter)) }, enabled = enabled && targetOperation.isNotBlank()) { Text("继续部署") }
                    OutlinedButton(onClick = { offer("取消未完成部署", "切换前终止暂存事务；已进入维护阶段则恢复原版本和一致性数据。已提交的成功部署不会被此按钮撤销。", "cancel_operation", JSONObject().put("target_operation_id", targetOperation).put("confirm_cancel", true)) }, enabled = enabled && targetOperation.isNotBlank()) { Text("取消并恢复") }
                }
                Text("未收到结果时先查询原操作，不要重复安装或写入。")
            }
        }
        item {
            DeploymentSection("现有节点与清理") {
                DeploymentInput("已有受管节点目录", adoptedPath) { adoptedPath = it.take(2048) }
                OutlinedButton(onClick = { offer("纳入已有受管节点", "只接受 Termux home 下具有 node.json 的兼容目录，核对已有进程身份后建立引用。未描述的旧目录不会被自动迁移或替换。", "adopt", JSONObject().put("existing_root", adoptedPath).put("confirm_adopt", true)) }, enabled = enabled && adoptedPath.startsWith('/')) { Text("确认接管目录引用") }
                Text("工作区迁移", style = MaterialTheme.typography.titleSmall)
                Text("新版默认工作区位于共享存储。检测到旧私有工作区时，先停止 Core，再经过复制、摘要校验和发布后删除私有副本。公共目录已有文件时不会自动合并。")
                DeploymentRow {
                    TextButton(onClick = { model.runTermux("workspace_migration_preview") }, enabled = enabled) { Text("预览旧工作区迁移") }
                    TextButton(onClick = {
                        val digest = migration?.optString("preview_digest").orEmpty()
                        offer("迁移旧私有工作区", "把旧私有工作区完整复制到用户可访问的共享目录，校验一致后删除私有副本。公共目标已有内容或 Core 正在运行时停止执行。",
                            "workspace_migrate", JSONObject().put("confirm_migration", true).put("preview_digest", digest))
                    }, enabled = enabled && migration?.let {
                        it.optBoolean("legacy_present") && it.optBoolean("target_empty") &&
                            it.optString("preview_digest").isNotBlank()
                    } == true) { Text("确认校验迁移") }
                }
                migration?.let {
                    Text("旧目录：${it.optString("legacy_path", "无")}\n公共目录：${it.optString("target_path", "未配置")}\n待迁移：${it.optBoolean("legacy_present")} · 目标为空：${it.optBoolean("target_empty")}\n受管旧布局：${it.optBoolean("managed_layout")} · 独立迁移制品：${it.optLong("artifact_bytes")} 字节 · 丢弃临时数据：${it.optLong("discarded_transient_bytes")} 字节")
                }
                Text("存储清理", style = MaterialTheme.typography.titleSmall)
                DeploymentRow {
                    TextButton(onClick = { model.runTermux("cleanup_preview") }, enabled = enabled) { Text("预览受管文件清理") }
                    TextButton(onClick = {
                        val digest = cleanup?.optString("preview_digest").orEmpty()
                        offer("清理预览中的受管文件", "清理无恢复引用的旧版本与备份、超过保留期的终态记录、诊断、私有临时文件，以及七天以上且仍停留在切换前阶段的废弃安装事务。公共工作区、身份、隔离数据和维护中的事务保留。", "cleanup", JSONObject().put("confirm_cleanup", true).put("preview_digest", digest))
                    }, enabled = enabled && !cleanup?.optString("preview_digest").isNullOrBlank()) { Text("确认预览清理") }
                }
                cleanup?.let { Text("旧版本：${it.optJSONArray("versions") ?: "[]"}\n旧备份：${it.optJSONArray("backups") ?: "[]"}\n废弃安装事务：${it.optJSONArray("abandoned_transactions") ?: "[]"}") }
                Text("完整卸载私有环境", style = MaterialTheme.typography.titleSmall)
                Text("该操作停止 Core，并删除 AgentDock 私有运行数据、桥接脚本及由本应用创建的 PRoot。共享工作区、公共制品与 Termux 公共依赖继续保留，随后可从系统设置卸载 APK。")
                DeploymentRow {
                    TextButton(onClick = { model.runTermux("uninstall_preview") }, enabled = enabled) { Text("预览完整卸载") }
                    TextButton(onClick = {
                        val digest = uninstall?.optString("preview_digest").orEmpty()
                        offer("完整卸载私有运行环境", "此操作会停止节点并删除 AgentDock 私有 Core、对话与任务数据、日志、备份、桥接脚本，以及明确标记为 AgentDock 创建的 PRoot。公共工作区、制品和共享 Termux 软件包保持不变。",
                            "uninstall", JSONObject().put("confirm_uninstall", true).put("preview_digest", digest))
                    }, enabled = enabled && !uninstall?.optString("preview_digest").isNullOrBlank()) { Text("确认完整卸载") }
                }
                uninstall?.let {
                    Text("保留工作区：${it.optString("workspace_path", "未配置")}\n保留制品：${it.optString("artifact_path", "未配置")}\n将删除自有 PRoot：${it.optString("managed_distro").ifBlank { "否" }} · 保留共享 Termux 依赖：${it.optBoolean("termux_packages_preserved")}")
                }
            }
        }
        item { Text("操作回执", style = MaterialTheme.typography.titleLarge) }
        if (state.operations.isEmpty()) item { Text("尚无操作。读取按钮只探测状态，安装与修改均需明确确认。") }
        items(state.operations, key = { it.operationId }) { operation ->
            Card(Modifier.fillMaxWidth()) {
                Column(Modifier.padding(12.dp), verticalArrangement = Arrangement.spacedBy(6.dp)) {
                    Text("${operation.operation} · ${operation.phase}")
                    Text(operation.operationId)
                    Text(operation.message)
                    if (operation.stdoutTruncated || operation.stderrTruncated) Text("输出被截断，先查询原操作。")
                    val data = remember(operation.resultJson) { runCatching { JSONObject(operation.resultJson) }.getOrNull() }
                    if (data != null) {
                        for (key in listOf("operation_id", "phase", "status", "source_version", "target_version", "retryable", "failure_code", "message")) {
                            if (data.has(key) && !data.isNull(key)) Text("$key：${data.opt(key)}")
                        }
                    }
                    TextButton(onClick = { targetOperation = data?.optString("operation_id")?.takeIf { it.isNotBlank() } ?: operation.operationId }) { Text("选为查询目标") }
                }
            }
        }
    }
}

@Composable
private fun DeploymentSection(title: String, content: @Composable ColumnScope.() -> Unit) {
    ElevatedCard(Modifier.fillMaxWidth()) {
        Column(Modifier.padding(14.dp), verticalArrangement = Arrangement.spacedBy(8.dp)) {
            Text(title, style = MaterialTheme.typography.titleMedium)
            content()
        }
    }
}

@Composable
private fun DeploymentInput(label: String, value: String, change: (String) -> Unit) {
    OutlinedTextField(value, change, label = { Text(label) }, modifier = Modifier.fillMaxWidth(), singleLine = true)
}

@Composable
private fun DeploymentRow(content: @Composable RowScope.() -> Unit) {
    Row(Modifier.fillMaxWidth().horizontalScroll(rememberScrollState()), horizontalArrangement = Arrangement.spacedBy(8.dp), content = content)
}
