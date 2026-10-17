package dev.agentdock.workbench.ui

import androidx.activity.compose.rememberLauncherForActivityResult
import androidx.activity.result.contract.ActivityResultContracts
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
import androidx.compose.ui.unit.dp
import org.json.JSONObject

@Composable
fun ProjectFilesManagementPage(state: WorkbenchUiState, model: WorkbenchViewModel, modifier: Modifier) {
    var importName by rememberSaveable { mutableStateOf("") }
    var conflict by rememberSaveable { mutableStateOf("fail") }
    var replaceConfirmed by rememberSaveable { mutableStateOf(false) }
    var exportName by rememberSaveable { mutableStateOf("") }
    var termuxPath by rememberSaveable { mutableStateOf("") }
    var termuxProjectName by rememberSaveable { mutableStateOf("") }
    var pathConfirmed by rememberSaveable { mutableStateOf(false) }
    var createConfirmed by rememberSaveable { mutableStateOf(false) }

    val projectPicker = rememberLauncherForActivityResult(ActivityResultContracts.OpenDocumentTree()) { uri ->
        if (uri != null) model.saveTreeUri("project", uri)
    }
    val artifactPicker = rememberLauncherForActivityResult(ActivityResultContracts.OpenDocumentTree()) { uri ->
        if (uri != null) model.saveTreeUri("artifact", uri)
    }
    val importPicker = rememberLauncherForActivityResult(ActivityResultContracts.OpenDocument()) { uri ->
        if (uri != null) {
            if (conflict == "replace" && !replaceConfirmed) model.showNotice("替换同名工程必须再次勾选确认。")
            else model.importProject(uri, importName, conflict)
        }
    }
    val exportPicker = rememberLauncherForActivityResult(ActivityResultContracts.CreateDocument("application/zip")) { uri ->
        if (uri != null && exportName.isNotBlank()) model.exportProject(uri, exportName)
    }

    LaunchedEffect(state.settings.projectTreeUri, state.fixture) {
        if (state.fixture || state.settings.projectTreeUri.isNotBlank()) model.refreshProjectRoots()
    }
    LaunchedEffect(state.selectedProjectName) {
        if (exportName.isBlank() && state.selectedProjectName.isNotBlank()) exportName = state.selectedProjectName
    }

    LazyColumn(modifier, verticalArrangement = Arrangement.spacedBy(10.dp)) {
        item {
            Text("项目与文件", style = MaterialTheme.typography.headlineSmall)
            Text("Android 仅访问用户通过系统 SAF 明确选择的目录。Termux 路径权限独立验证，两者不会被推断为同一授权。")
        }
        item {
            ResourceSection("目录授权") {
                Text("项目目录：${state.settings.projectTreeUri.ifBlank { "尚未选择" }}")
                Text("产物目录：${state.settings.artifactTreeUri.ifBlank { "尚未选择" }}")
                ResourceActions {
                    Button(onClick = { projectPicker.launch(null) }, enabled = !state.projectBusy && !state.fixture) { Text("选择项目目录") }
                    OutlinedButton(onClick = { artifactPicker.launch(null) }, enabled = !state.projectBusy && !state.fixture) { Text("选择产物目录") }
                    TextButton(onClick = model::refreshProjectRoots, enabled = !state.projectBusy) { Text("刷新工程") }
                }
                if (state.fixture) Text("Fixture 模式只显示固定目录，不申请或修改真实 SAF 权限。")
                if (state.projectMessage.isNotBlank()) Text(state.projectMessage)
            }
        }
        item {
            ResourceSection("ZIP 导入") {
                ResourceField("导入后的工程名称", importName, { importName = it.take(128) })
                PermissionChoice("同名处理", listOf("fail" to "拒绝写入", "rename" to "生成新名称", "replace" to "备份后替换"), conflict) {
                    conflict = it
                    replaceConfirmed = false
                }
                if (conflict == "replace") Row {
                    Checkbox(replaceConfirmed, { replaceConfirmed = it })
                    Text("确认在临时工程完整写入后替换同名目录；失败时恢复原目录", Modifier.padding(top = 12.dp))
                }
                Button(
                    onClick = { importPicker.launch(arrayOf("application/zip", "application/octet-stream")) },
                    enabled = !state.projectBusy && !state.fixture && state.settings.projectTreeUri.isNotBlank() && importName.isNotBlank() && (conflict != "replace" || replaceConfirmed)
                ) { Text("选择 ZIP 并导入") }
                Text("先在应用缓存校验路径、重复项、成员类型、文件数量和大小，再写入 SAF 临时目录。发布前不会覆盖同名工程。")
            }
        }
        item {
            ResourceSection("工程目录") {
                if (state.projectRoots.isEmpty()) Text("当前没有工程目录，或尚未读取。")
                state.projectRoots.forEach { project ->
                    TextButton(onClick = { model.selectProject(project.name); exportName = project.name }) {
                        Text(if (state.selectedProjectName == project.name) "● ${project.name}" else project.name)
                    }
                }
            }
        }
        if (state.selectedProjectName.isNotBlank()) item {
            ResourceSection("文件树 · ${state.selectedProjectName}") {
                Text("页面最多展示 500 项；大工程应直接导出后在受控环境检查。")
                if (state.projectFiles.isEmpty()) Text("工程为空，或文件树尚未读取。")
                state.projectFiles.forEach { entry ->
                    if (entry.directory) Text("📁 ${entry.relativePath}")
                    else TextButton(onClick = { model.previewProjectFile(entry.relativePath) }) {
                        Text("${entry.relativePath} · ${if (entry.sizeBytes >= 0) entry.sizeBytes else "?"} B")
                    }
                }
                if (state.projectPreview.isNotBlank()) {
                    Text("有界文本预览", style = MaterialTheme.typography.titleSmall)
                    Text(state.projectPreview)
                }
            }
        }
        item {
            ResourceSection("ZIP 导出") {
                ResourceField("工程名称", exportName, { exportName = it.take(128) })
                Button(
                    onClick = { exportPicker.launch("${exportName.ifBlank { "AgentDock-Project" }}.zip") },
                    enabled = !state.projectBusy && !state.fixture && state.settings.projectTreeUri.isNotBlank() && exportName.isNotBlank()
                ) { Text("导出工程 ZIP") }
                Text("导出只遍历所选工程，限制为 10,000 项和 512 MiB；失败时尝试删除未完成目标。")
            }
        }
        item {
            ResourceSection("Termux 路径独立验证") {
                ResourceField("Termux 绝对路径", termuxPath, { termuxPath = it.take(2048) })
                Row {
                    Checkbox(pathConfirmed, { pathConfirmed = it })
                    Text("确认允许外部 Termux 对该目录执行一次创建、回读和删除探针", Modifier.padding(top = 12.dp))
                }
                ResourceActions {
                    OutlinedButton(onClick = {
                        model.runTermux("path_probe", JSONObject().put("termux_path", termuxPath).put("confirm_path", true))
                    }, enabled = !state.actionBusy && pathConfirmed && termuxPath.startsWith('/')) { Text("验证 Termux 路径") }
                }
                ResourceField("新建工程名称", termuxProjectName, { termuxProjectName = it.take(128) })
                Row {
                    Checkbox(createConfirmed, { createConfirmed = it })
                    Text("确认在已授权 Termux 路径下创建工程目录和最小 AGENTS.md", Modifier.padding(top = 12.dp))
                }
                Button(onClick = {
                    model.runTermux("project_create", JSONObject()
                        .put("termux_path", termuxPath).put("confirm_path", true).put("name", termuxProjectName))
                }, enabled = !state.actionBusy && createConfirmed && termuxPath.startsWith('/') && termuxProjectName.isNotBlank()) { Text("创建 Termux 工程") }
                Text("SAF URI 与 Termux 路径分别授权。路径探针成功不会自动登记 Core 工作区；工作区登记仍在“工作区”页面完成。")
            }
        }
        item {
            ResourceSection("边界") {
                Text("导入/导出不读取 Core 私有数据库、身份或 OAuth 材料，不扫描整个共享存储，也不使用提权、调试或自动化旁路访问文件。")
                Text("本候选未在真实手机目录上执行任何导入、导出或路径探针。")
            }
        }
    }
}
