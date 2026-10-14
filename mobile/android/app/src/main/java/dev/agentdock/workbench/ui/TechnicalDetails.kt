package dev.agentdock.workbench.ui

import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.ColumnScope
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Text
import androidx.compose.material3.TextButton
import androidx.compose.runtime.*
import androidx.compose.runtime.saveable.rememberSaveable
import androidx.compose.ui.Modifier
import androidx.compose.ui.unit.dp

@Composable
internal fun TechnicalDetails(title: String = "技术详情", content: @Composable ColumnScope.() -> Unit) {
    var expanded by rememberSaveable(title) { mutableStateOf(false) }
    Column(Modifier.fillMaxWidth(), verticalArrangement = Arrangement.spacedBy(6.dp)) {
        TextButton(onClick = { expanded = !expanded }) { Text(if (expanded) "收起$title" else "查看$title") }
        if (expanded) content()
    }
}

internal fun userStatus(value: String): String = when (value.lowercase()) {
    "healthy", "running" -> "运行中"; "starting" -> "启动中"; "stopped" -> "已停止"; "degraded" -> "需要检查"
    "unknown" -> "待确认"; "queued" -> "等待中"; "succeeded", "completed" -> "已完成"; "failed" -> "失败"
    "cancelled" -> "已取消"; "requires_user_action" -> "需要处理"; "active" -> "进行中"; else -> value
}
internal fun userOperation(value: String): String = when (value) {
    "probe", "status" -> "检测运行环境"; "install", "bootstrap" -> "准备运行环境"; "start" -> "启动节点"; "stop" -> "停止节点"
    "restart" -> "重启节点"; "update" -> "更新节点"; "repair" -> "修复环境"; "pair_local_core" -> "连接本机节点"
    "export_diagnostics" -> "生成诊断包"; "operation_query" -> "查询原操作"; "cleanup_preview", "cleanup" -> "清理受管文件"
    "workspace_migration_preview", "workspace_migrate" -> "迁移公共工作区"; "uninstall_preview", "uninstall" -> "卸载私有环境"; else -> value
}
