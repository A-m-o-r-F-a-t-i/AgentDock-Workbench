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
import androidx.lifecycle.compose.collectAsStateWithLifecycle
import dev.agentdock.workbench.data.CorePages
import dev.agentdock.workbench.data.ManagementContract
import dev.agentdock.workbench.data.WorkbenchCommands
import org.json.JSONObject

@Composable
fun SkillManagementPage(state: WorkbenchUiState, model: WorkbenchViewModel, modifier: Modifier) {
    val resources by model.resources.state.collectAsStateWithLifecycle()
    val prompt = rememberCommandPrompt(model)
    var selectedRef by rememberSaveable { mutableStateOf("") }
    var selectedName by rememberSaveable { mutableStateOf("") }
    var selectedPlugin by rememberSaveable { mutableStateOf("") }
    var filePath by rememberSaveable(selectedRef) { mutableStateOf("SKILL.md") }

    fun reload() = model.resources.read("skills", "/internal/runtime/skills?summary=true")
    fun skillRoute(suffix: String = ""): String {
        val name = ManagementContract.encode(selectedName)
        val query = "skill_ref=${ManagementContract.encode(selectedRef)}"
        return "/internal/runtime/skills/$name$suffix?$query"
    }
    fun select(row: JSONObject) {
        selectedName = row.optString("skill", row.optString("name"))
        selectedRef = row.optString("skill_ref")
        selectedPlugin = row.optString("plugin_name", row.optString("plugin"))
        model.resources.read("skill-detail", skillRoute())
        model.resources.read("skill-files", skillRoute("/files"))
    }
    fun readFile() {
        val clean = filePath.split('/').filter { it.isNotBlank() && it != "." }
        require(clean.isNotEmpty() && clean.none { it == ".." })
        val encoded = clean.joinToString("/") { ManagementContract.encode(it) }
        model.resources.read("skill-file", skillRoute("/files/$encoded"))
    }
    LaunchedEffect(state.settings.endpoint, state.settings.remoteEndpointEnabled) { reload() }

    val list = resources.views["skills"]
    val rows = remember(list?.data) { runCatching { list?.data?.let { CorePages.rows(it, "skills") }.orEmpty() } }
    val detail = resources.views["skill-detail"]?.data
    val filesEnvelope = resources.views["skill-files"]?.data
    val files = remember(filesEnvelope) { runCatching { filesEnvelope?.let { CorePages.rows(it, "files", 2_000) }.orEmpty() } }
    val file = resources.views["skill-file"]?.data

    LazyColumn(modifier, verticalArrangement = Arrangement.spacedBy(10.dp)) {
        item {
            Text("Skill 管理", style = MaterialTheme.typography.headlineSmall)
            Text("列表使用 Core 签发的 skill_ref。详情和文件读取固定该引用，不按显示名称重新解析来源。")
            TextButton(onClick = ::reload) { Text("刷新库存") }
            ResourceFeedback(list, ::reload)
            rows.exceptionOrNull()?.let { Text(it.message.orEmpty()) }
            if (list?.data != null && rows.getOrDefault(emptyList()).isEmpty()) Text("当前没有可用 Skill。")
        }
        items(rows.getOrDefault(emptyList()), key = { it.optString("skill_ref", it.optString("skill")) }) { row ->
            val reference = row.optString("skill_ref")
            Card(Modifier.fillMaxWidth()) {
                Row(Modifier.padding(12.dp), horizontalArrangement = Arrangement.spacedBy(8.dp)) {
                    Column(Modifier.weight(1f)) {
                        Text(row.optString("name", row.optString("skill", reference)), style = MaterialTheme.typography.titleMedium)
                        ResourceObject(row, listOf("skill", "skill_ref", "description", "source_type", "source_id", "plugin_name", "enabled", "bundled", "active_version", "file_count", "content_digest"))
                    }
                    TextButton(onClick = { select(row) }) { Text(if (selectedRef == reference) "刷新详情" else "详情") }
                }
            }
        }
        if (selectedRef.isNotBlank()) item {
            ResourceSection("Skill 详情") {
                ResourceFeedback(resources.views["skill-detail"]) {
                    model.resources.read("skill-detail", skillRoute())
                }
                detail?.let { value ->
                    ResourceObject(value, listOf("skill", "name", "skill_ref", "source_type", "source_id", "plugin_name", "version", "active_version", "enabled", "bundled", "file_count", "content_digest"))
                    value.optJSONObject("document")?.let { ResourceObject(it, listOf("name", "description", "version")) }
                }
                ResourceActions {
                    Button(onClick = {
                        prompt.ask({
                            if (selectedPlugin.isBlank()) WorkbenchCommands.skill(selectedRef, true)
                            else WorkbenchCommands.plugin("member_enable", selectedPlugin, memberType = "skill", member = selectedName)
                        }) { reload() }
                    }, enabled = !resources.actionBusy) { Text("启用") }
                    OutlinedButton(onClick = {
                        prompt.ask({
                            if (selectedPlugin.isBlank()) WorkbenchCommands.skill(selectedRef, false)
                            else WorkbenchCommands.plugin("member_disable", selectedPlugin, memberType = "skill", member = selectedName)
                        }) { reload() }
                    }, enabled = !resources.actionBusy) { Text("停用") }
                }
            }
        }
        if (selectedRef.isNotBlank()) item {
            ResourceSection("文件浏览") {
                ResourceFeedback(resources.views["skill-files"]) {
                    model.resources.read("skill-files", skillRoute("/files"))
                }
                files.exceptionOrNull()?.let { Text(it.message.orEmpty()) }
                files.getOrDefault(emptyList()).take(2_000).forEach { entry ->
                    TextButton(onClick = {
                        filePath = entry.optString("path", entry.optString("name"))
                        readFile()
                    }) {
                        Text("${entry.optString("path", entry.optString("name"))} · ${entry.optLong("size_bytes", -1)} B")
                    }
                }
                ResourceField("相对文件路径", filePath, { filePath = it.take(2048) })
                Button(onClick = ::readFile, enabled = filePath.isNotBlank()) { Text("读取文件") }
                ResourceFeedback(resources.views["skill-file"], ::readFile)
                file?.let { value ->
                    ResourceObject(value, listOf("skill_ref", "path", "encoding", "size_bytes"))
                    val content = value.optString("content", value.optString("file"))
                    if (content.isNotBlank()) Text(content.take(100_000))
                }
            }
        }
        item { ResourceReceipt(resources.actionResult) }
    }
}
