package dev.agentdock.workbench.ui

import android.app.Application
import android.content.Intent
import android.net.Uri
import androidx.lifecycle.AndroidViewModel
import androidx.lifecycle.SavedStateHandle
import androidx.lifecycle.ViewModel
import androidx.lifecycle.ViewModelProvider
import androidx.lifecycle.createSavedStateHandle
import androidx.lifecycle.viewmodel.CreationExtras
import androidx.lifecycle.viewModelScope
import dev.agentdock.workbench.BuildConfig
import dev.agentdock.workbench.WorkbenchApplication
import dev.agentdock.workbench.data.ListQuery
import dev.agentdock.workbench.data.ManagementContract
import dev.agentdock.workbench.data.SafEntry
import dev.agentdock.workbench.lifecycle.GuardianScheduler
import dev.agentdock.workbench.model.*
import kotlinx.coroutines.CancellationException
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.Job
import kotlinx.coroutines.delay
import kotlinx.coroutines.flow.*
import kotlinx.coroutines.launch
import kotlinx.coroutines.withContext
import org.json.JSONObject
import java.util.UUID

/** Only transient client presentation is saved here. Core owns every business state. */
data class WorkbenchUiState(
    val settings: WorkbenchSettings = WorkbenchSettings(),
    val snapshot: WorkbenchSnapshot = WorkbenchSnapshot(),
    val screen: WorkbenchScreen = WorkbenchScreen.Home,
    val selectedConversationId: String = "",
    val selectedCallId: String = "",
    val selectedTaskId: String = "",
    val selectedApprovalId: String = "",
    val tasksQuery: ListQuery = ListQuery(),
    val conversationsQuery: ListQuery = ListQuery(),
    val checkedIds: Set<String> = emptySet(),
    val detail: JSONObject? = null,
    val detailKind: String = "",
    val detailId: String = "",
    val detailError: String = "",
    val payload: JSONObject? = null,
    val payloadKind: String = "response",
    val payloadCallId: String = "",
    val children: List<WorkbenchItem> = emptyList(),
    val batchResult: JSONObject? = null,
    val insertionDraft: String = "",
    val loading: Boolean = false,
    val actionBusy: Boolean = false,
    val message: String = "",
    val liveStatus: String = "尚未连接活动流",
    val fixture: Boolean = false,
    val operations: List<BridgeOperation> = emptyList(),
    val projectRoots: List<SafEntry> = emptyList(),
    val projectFiles: List<SafEntry> = emptyList(),
    val selectedProjectName: String = "",
    val projectPreview: String = "",
    val projectMessage: String = "",
    val projectBusy: Boolean = false,
    val credentialAvailable: Boolean = false,
    val oauthConfigured: Boolean = false,
    val oauthValid: Boolean = false,
    val oauthExpiresAtEpochMs: Long = 0L,
    val oauthIssuer: String = "",
    val buildIdentity: String = "${BuildConfig.PRODUCT_VERSION} · ${BuildConfig.CANDIDATE_SHA.take(12)} · ${BuildConfig.SIGNING_LABEL}"
)

private data class CredentialUiSnapshot(
    val available: Boolean,
    val oauthConfigured: Boolean,
    val oauthValid: Boolean,
    val oauthExpiresAtEpochMs: Long,
    val oauthIssuer: String
)

class WorkbenchViewModel(
    application: Application,
    fixtureRequested: Boolean,
    private val saved: SavedStateHandle = SavedStateHandle()
) : AndroidViewModel(application) {
    private val graph = (application as WorkbenchApplication).graph
    private val _state = MutableStateFlow(WorkbenchUiState(
        fixture = fixtureRequested && BuildConfig.DEBUG,
        screen = WorkbenchScreen.fromRoute(saved["route"]),
        selectedConversationId = saved["conversation"] ?: "",
        selectedTaskId = saved["task"] ?: "",
        selectedCallId = saved["call"] ?: "",
        selectedApprovalId = saved["approval"] ?: "",
        tasksQuery = restoreQuery("tasks"), conversationsQuery = restoreQuery("conversations"),
        insertionDraft = saved["draft"] ?: ""
    ))
    val state: StateFlow<WorkbenchUiState> = _state.asStateFlow()
    val resources = CoreResourceController(viewModelScope, { graph.repository.managementClient() },
        { _state.value.fixture }, { _state.value.snapshot }, ::showNotice, ::refresh)

    private val activityStream = RestartableCoreStream(viewModelScope)
    private val callStream = RestartableCoreStream(viewModelScope)
    private var refreshJob: Job? = null
    private var detailJob: Job? = null
    private var payloadJob: Job? = null
    private var payloadGeneration = 0L
    private var refreshDebounce: Job? = null
    private var refreshGeneration = 0L

    init {
        viewModelScope.launch {
            graph.settings.settings.collectLatest { settings ->
                _state.update { it.copy(settings = settings) }
                if (!_state.value.fixture) GuardianScheduler.configure(getApplication(), settings)
            }
        }
        viewModelScope.launch {
            graph.operations.changes.collectLatest {
                val records = withContext(Dispatchers.IO) { graph.operations.list() }
                _state.update { it.copy(operations = records) }
            }
        }
        refresh()
    }

    fun navigate(screen: WorkbenchScreen) {
        val current = _state.value.screen
        if (current == screen) return
        val history = (saved.get<ArrayList<String>>("history") ?: arrayListOf()).toMutableList()
        history.add(current.route)
        saved["history"] = ArrayList(history.takeLast(24))
        saved["route"] = screen.route
        _state.update { it.copy(screen = screen, checkedIds = emptySet()) }
    }

    fun back() {
        val history = (saved.get<ArrayList<String>>("history") ?: arrayListOf()).toMutableList()
        val route = if (history.isEmpty()) WorkbenchScreen.Home.route else history.removeAt(history.lastIndex)
        saved["history"] = ArrayList(history)
        saved["route"] = route
        _state.update { it.copy(screen = WorkbenchScreen.fromRoute(route), checkedIds = emptySet()) }
    }

    fun selectWorkspace(item: WorkbenchItem) {
        updateQuery("tasks", _state.value.tasksQuery.copy(workspaceId = item.id, offset = 0), false)
        updateQuery("conversations", _state.value.conversationsQuery.copy(workspaceId = item.id, offset = 0), false)
        navigate(WorkbenchScreen.Conversations)
        refresh()
    }

    fun selectConversation(item: WorkbenchItem) {
        if (item.id != _state.value.selectedConversationId) {
            saved["draft"] = ""
            saved["submission_id"] = ""
        }
        saved["conversation"] = item.id
        _state.update { it.copy(selectedConversationId = item.id, insertionDraft = saved["draft"] ?: "") }
        navigate(WorkbenchScreen.Conversations)
        loadDetail("conversations", item.id)
        refresh()
    }

    fun selectTask(item: WorkbenchItem) {
        saved["task"] = item.id
        _state.update { it.copy(selectedTaskId = item.id) }
        navigate(WorkbenchScreen.Tasks)
        loadDetail("tasks", item.id)
    }

    fun selectCall(item: WorkbenchItem) {
        saved["call"] = item.id
        _state.update { it.copy(selectedCallId = item.id, payload = null, children = emptyList()) }
        navigate(WorkbenchScreen.CallDetail)
        loadDetail("calls", item.id)
    }

    fun selectApproval(item: WorkbenchItem) {
        saved["approval"] = item.id
        _state.update { it.copy(selectedApprovalId = item.id) }
        navigate(WorkbenchScreen.Approvals)
        loadDetail("approvals", item.id)
    }

    fun updateQuery(kind: String, value: ListQuery, reload: Boolean = true) {
        for ((key, entry) in mapOf("view" to value.view, "search" to value.search, "status" to value.status,
            "workspace" to value.workspaceId, "tag" to value.tag)) saved["${kind}_$key"] = entry
        saved["${kind}_offset"] = value.offset
        _state.update { if (kind == "tasks") it.copy(tasksQuery = value, checkedIds = emptySet()) else it.copy(conversationsQuery = value, checkedIds = emptySet()) }
        if (reload) refresh()
    }

    fun toggleChecked(identity: String) {
        _state.update {
            val ids = if (identity in it.checkedIds) it.checkedIds - identity else it.checkedIds + identity
            it.copy(checkedIds = ids.take(ManagementContract.MAX_BATCH).toSet())
        }
    }

    fun checkPage(kind: String) {
        val items = if (kind == "tasks") _state.value.snapshot.tasks else _state.value.snapshot.conversations
        _state.update { it.copy(checkedIds = items.map { row -> row.id }.toSet()) }
    }

    fun manage(kind: String, ids: List<String>, action: String, title: String = "", tags: List<String> = emptyList(), confirmed: Boolean = false) = perform {
        val outcome = graph.repository.batch(kind, ids.toList(), action, title, tags, confirmed)
        _state.update { it.copy(batchResult = outcome.serverValue) }
        outcome
    }

    fun clearMessage() = _state.update { it.copy(message = "") }

    fun refresh() {
        refreshJob?.cancel()
        val generation = ++refreshGeneration
        refreshJob = viewModelScope.launch {
            _state.update { it.copy(loading = true) }
            val selected = _state.value
            try {
                val received = graph.repository.refresh(selected.fixture, selected.selectedConversationId, selected.tasksQuery, selected.conversationsQuery)
                val snapshot = if (selected.fixture) fixtureSnapshot(received) else received
                if (generation != refreshGeneration) return@launch
                val operations = withContext(Dispatchers.IO) { graph.operations.list() }
                val credential = if (selected.fixture) CredentialUiSnapshot(false, false, false, 0L, "") else withContext(Dispatchers.IO) {
                    val settings = graph.settings.current()
                    val origin = dev.agentdock.workbench.data.EndpointPolicy.resolve(settings.endpoint, settings.remoteEndpointEnabled)
                    val oauth = graph.credentials.oauthStatus(origin)
                    CredentialUiSnapshot(
                        available = graph.credentials.getCore(origin).isNotBlank(),
                        oauthConfigured = oauth.configured,
                        oauthValid = oauth.valid,
                        oauthExpiresAtEpochMs = oauth.expiresAtEpochMs,
                        oauthIssuer = oauth.issuer
                    )
                }
                _state.update { it.copy(
                    snapshot = snapshot,
                    loading = false,
                    operations = operations,
                    credentialAvailable = credential.available,
                    oauthConfigured = credential.oauthConfigured,
                    oauthValid = credential.oauthValid,
                    oauthExpiresAtEpochMs = credential.oauthExpiresAtEpochMs,
                    oauthIssuer = credential.oauthIssuer
                ) }
                if (!selected.fixture && snapshot.coreHealth in setOf(NodeHealth.Healthy, NodeHealth.Degraded)) startStreams()
            } catch (error: CancellationException) {
                throw error
            } catch (error: Exception) {
                if (generation == refreshGeneration) _state.update { it.copy(loading = false, message = ManagementContract.failure(error)) }
            }
        }
    }

    private fun loadDetail(kind: String, identity: String) {
        detailJob?.cancel()
        _state.update { it.copy(detail = null, detailKind = kind, detailId = identity, detailError = "") }
        if (_state.value.fixture) return
        detailJob = viewModelScope.launch {
            try {
                val detail = graph.repository.detail(kind, identity)
                if (_state.value.detailId == identity && _state.value.detailKind == kind) _state.update { it.copy(detail = detail) }
            } catch (error: CancellationException) { throw error
            } catch (error: Exception) { _state.update { it.copy(detailError = ManagementContract.failure(error)) } }
        }
    }

    fun loadPayload(kind: String, offset: Long = 0) = loadCallPayload(_state.value.selectedCallId, kind, offset)

    fun loadCallPayload(identity: String, kind: String, offset: Long = 0) {
        if (_state.value.fixture) return
        payloadJob?.cancel()
        val generation = ++payloadGeneration
        payloadJob = viewModelScope.launch {
            try {
                val value = graph.repository.callPayload(identity, kind, offset)
                if (generation == payloadGeneration) _state.update { it.copy(payload = value, payloadKind = kind, payloadCallId = identity, detailError = "") }
            } catch (error: CancellationException) { throw error
            } catch (error: Exception) { if (generation == payloadGeneration) _state.update { it.copy(detailError = ManagementContract.failure(error)) } }
        }
    }

    fun updateSettings(transform: (WorkbenchSettings) -> WorkbenchSettings) {
        viewModelScope.launch { try { graph.settings.update(transform) } catch (error: CancellationException) { throw error } catch (error: Exception) { showError(error) } }
    }

    fun saveConnection(endpoint: String, remoteEnabled: Boolean, bearer: String) {
        if (_state.value.fixture) { fixtureBlocked(); return }
        viewModelScope.launch {
            try {
                val origin = dev.agentdock.workbench.data.EndpointPolicy.resolve(endpoint, remoteEnabled)
                stopStreams()
                refreshJob?.cancel()
                detailJob?.cancel()
                withContext(Dispatchers.IO) { if (bearer.isNotBlank()) graph.credentials.putCore(origin, bearer.trim()) }
                graph.settings.update { it.copy(endpoint = endpoint.trim().trimEnd('/'), remoteEndpointEnabled = remoteEnabled) }
                refresh()
            } catch (error: CancellationException) { throw error } catch (error: Exception) { showError(error) }
        }
    }

    fun clearBearer() {
        if (_state.value.fixture) { fixtureBlocked(); return }
        stopStreams()
        refreshJob?.cancel()
        detailJob?.cancel()
        viewModelScope.launch {
            withContext(Dispatchers.IO) { graph.credentials.clearCoreCredentials() }
            _state.update {
                it.copy(message = "本机保存的手工、本机配对和 OAuth 凭据均已删除；服务器端授权未伪称已撤销")
            }
            refresh()
        }
    }

    fun pairLocalCore() {
        if (_state.value.fixture) { fixtureBlocked(); return }
        if (_state.value.actionBusy) return
        _state.update { it.copy(actionBusy = true) }
        viewModelScope.launch {
            try {
                val settings = graph.settings.current()
                val origin = dev.agentdock.workbench.data.EndpointPolicy.resolve(settings.endpoint, settings.remoteEndpointEnabled)
                check(dev.agentdock.workbench.termux.LocalCorePairingManager.loopbackOrigin(origin)) {
                    "一次性本机配对只支持字面 loopback Origin；远程 Core 请使用其 OAuth/管理授权流程。"
                }
                val pending = withContext(Dispatchers.IO) { graph.termux.dispatchLocalPairing(origin) }
                val operations = withContext(Dispatchers.IO) { graph.operations.list() }
                _state.update {
                    it.copy(
                        operations = operations,
                        message = "已提交一次性公钥配对 ${pending.operationId}；成功回执会自动写入与当前 Origin 绑定的 Keystore 密文。"
                    )
                }
            } catch (error: CancellationException) { throw error
            } catch (error: Exception) { showError(error)
            } finally { _state.update { it.copy(actionBusy = false) } }
        }
    }

    fun pairRemoteOAuth() {
        if (_state.value.fixture) { fixtureBlocked(); return }
        if (_state.value.actionBusy) return
        _state.update { it.copy(actionBusy = true) }
        viewModelScope.launch {
            var pending: dev.agentdock.workbench.data.OAuthPendingSession? = null
            try {
                val settings = graph.settings.current()
                val origin = dev.agentdock.workbench.data.EndpointPolicy.resolve(settings.endpoint, settings.remoteEndpointEnabled)
                val session = withContext(Dispatchers.IO) { graph.remoteOAuth.begin(origin) }
                pending = session
                dev.agentdock.workbench.data.OAuthBrowserLauncher.open(getApplication(), session.authorizationUrl)
                val status = withContext(Dispatchers.IO) { graph.remoteOAuth.awaitAndStore(session) }
                stopStreams()
                _state.update {
                    it.copy(
                        message = "远程 Core OAuth/PKCE 配对完成；访问令牌已绑定当前 Origin，过期时间 ${status.expiresAtEpochMs}。"
                    )
                }
                refresh()
            } catch (error: CancellationException) { throw error
            } catch (error: Exception) { showError(error)
            } finally {
                pending?.let(graph.remoteOAuth::cancel)
                _state.update { it.copy(actionBusy = false) }
            }
        }
    }

    fun setInsertionDraft(text: String) {
        if (text.toByteArray(Charsets.UTF_8).size > 8192) return
        if (text != _state.value.insertionDraft) saved["submission_id"] = ""
        saved["draft"] = text
        _state.update { it.copy(insertionDraft = text) }
    }

    fun sendInsertion(text: String) = perform {
        val conversation = requireConversation()
        val submission = saved.get<String>("submission_id").orEmpty().ifBlank {
            UUID.randomUUID().toString().replace("-", "").also { saved["submission_id"] = it }
        }
        val result = graph.repository.sendInsertion(conversation, text, submission)
        if (result.accepted && _state.value.selectedConversationId == conversation && _state.value.insertionDraft == text) setInsertionDraft("")
        result
    }

    fun insertionAction(insertionId: String, action: String) = perform { graph.repository.insertionAction(requireConversation(), insertionId, action) }
    fun terminateConversation() = perform { graph.repository.terminateConversation(requireConversation()) }
    fun callAction(action: String) = perform { graph.repository.callAction(_state.value.selectedCallId.ifBlank { error("请先选择调用") }, action) }
    fun approvalAction(approve: Boolean, allowWorkspace: Boolean = false) = perform {
        graph.repository.approvalAction(_state.value.selectedApprovalId.ifBlank { error("请先选择审批") }, approve, allowWorkspace)
    }
    fun capabilityAction(kind: String, item: WorkbenchItem, enable: Boolean) = perform { graph.repository.capabilityAction(kind, item.id, enable) }

    fun runTermux(operation: String, arguments: JSONObject = JSONObject()) = perform {
        val payload = JSONObject(arguments.toString()).put("source", "android_ui")
        if (operation in setOf("install", "update")) payload.put("apk_version", BuildConfig.PRODUCT_VERSION)
        val pending = withContext(Dispatchers.IO) { graph.termux.dispatch(operation, payload) }
        ActionOutcome(true, "queued", "已提交 ${pending.operation}；结果以 Termux 回执为准")
    }

    fun setGuardianEnabled(enabled: Boolean) {
        if (_state.value.fixture) { fixtureBlocked(); return }
        viewModelScope.launch {
            try {
                graph.guardian.setEnabled(enabled)
            } catch (error: CancellationException) { throw error } catch (error: Exception) { showError(error) }
        }
    }

    fun setNotificationsEnabled(enabled: Boolean) {
        if (_state.value.fixture) { fixtureBlocked(); return }
        viewModelScope.launch {
            try {
                graph.guardian.setNotifications(enabled)
                showNotice(if (enabled) "通知开关已启用；Android 13+ 仍需系统授权。" else "通知和节点守护均已关闭；Core 未被停止。")
            } catch (error: CancellationException) { throw error
            } catch (error: Exception) { showError(error) }
        }
    }

    fun setGuardianPaused(paused: Boolean) {
        if (_state.value.fixture) { fixtureBlocked(); return }
        viewModelScope.launch {
            try {
                graph.guardian.setPaused(paused)
            } catch (error: CancellationException) { throw error } catch (error: Exception) { showError(error) }
        }
    }

    fun saveTreeUri(kind: String, uri: Uri) {
        if (_state.value.fixture) { fixtureBlocked(); return }
        viewModelScope.launch {
            try {
                getApplication<Application>().contentResolver.takePersistableUriPermission(
                    uri,
                    Intent.FLAG_GRANT_READ_URI_PERMISSION or Intent.FLAG_GRANT_WRITE_URI_PERMISSION
                )
                graph.settings.update {
                    when (kind) {
                        "project" -> it.copy(projectTreeUri = uri.toString())
                        "artifact" -> it.copy(artifactTreeUri = uri.toString())
                        else -> error("未知目录类型")
                    }
                }
                if (kind == "project") refreshProjectRoots()
                showNotice("已保存用户明确选择的 ${if (kind == "project") "项目" else "产物"}目录授权。")
            } catch (error: CancellationException) { throw error
            } catch (error: Exception) { showError(error) }
        }
    }

    fun refreshProjectRoots() {
        if (_state.value.fixture) {
            _state.update { it.copy(
                projectRoots = listOf(SafEntry("Android Demo", "Android Demo", "fixture://project", true, -1, "vnd.android.document/directory")),
                projectMessage = "Fixture 工程目录；不读取真实 SAF。"
            ) }
            return
        }
        if (_state.value.projectBusy) return
        viewModelScope.launch {
            _state.update { it.copy(projectBusy = true, projectMessage = "正在读取项目目录…") }
            try {
                val settings = graph.settings.current()
                val projects = withContext(Dispatchers.IO) { graph.projects.listProjects(settings.projectTreeUri) }
                _state.update { it.copy(
                    projectBusy = false,
                    projectRoots = projects,
                    projectFiles = if (it.selectedProjectName in projects.map(SafEntry::name)) it.projectFiles else emptyList(),
                    selectedProjectName = it.selectedProjectName.takeIf { selected -> selected in projects.map(SafEntry::name) }.orEmpty(),
                    projectPreview = "",
                    projectMessage = "读取到 ${projects.size} 个工程目录。"
                ) }
            } catch (error: CancellationException) { throw error
            } catch (error: Exception) {
                _state.update { it.copy(projectBusy = false, projectMessage = ManagementContract.failure(error)) }
            }
        }
    }

    fun selectProject(name: String) {
        if (_state.value.fixture) {
            _state.update { it.copy(
                selectedProjectName = name,
                projectFiles = listOf(
                    SafEntry("README.md", "README.md", "fixture://readme", false, 128, "text/markdown"),
                    SafEntry("src", "src", "fixture://src", true, -1, "vnd.android.document/directory"),
                    SafEntry("main.c", "src/main.c", "fixture://main", false, 256, "text/x-csrc")
                ),
                projectPreview = "",
                projectMessage = "Fixture 文件树；不读取真实 SAF。"
            ) }
            return
        }
        if (_state.value.projectBusy) return
        viewModelScope.launch {
            _state.update { it.copy(projectBusy = true, selectedProjectName = name, projectPreview = "", projectMessage = "正在读取工程…") }
            try {
                val settings = graph.settings.current()
                val files = withContext(Dispatchers.IO) { graph.projects.listProject(settings.projectTreeUri, name) }
                _state.update { it.copy(projectBusy = false, projectFiles = files, projectMessage = "工程包含 ${files.size} 个有界展示项。") }
            } catch (error: CancellationException) { throw error
            } catch (error: Exception) {
                _state.update { it.copy(projectBusy = false, projectFiles = emptyList(), projectMessage = ManagementContract.failure(error)) }
            }
        }
    }

    fun previewProjectFile(relativePath: String) {
        if (_state.value.fixture) {
            _state.update { it.copy(projectPreview = "# Fixture preview\n\n$relativePath\nNo real file was read.") }
            return
        }
        val project = _state.value.selectedProjectName
        if (project.isBlank() || _state.value.projectBusy) return
        viewModelScope.launch {
            _state.update { it.copy(projectBusy = true, projectPreview = "", projectMessage = "正在读取文本预览…") }
            try {
                val settings = graph.settings.current()
                val text = withContext(Dispatchers.IO) { graph.projects.readText(settings.projectTreeUri, project, relativePath) }
                _state.update { it.copy(projectBusy = false, projectPreview = text, projectMessage = "已读取最多 100,000 字节的文本预览。") }
            } catch (error: CancellationException) { throw error
            } catch (error: Exception) {
                _state.update { it.copy(projectBusy = false, projectMessage = ManagementContract.failure(error)) }
            }
        }
    }

    fun importProject(uri: Uri, name: String, conflict: String) {
        if (_state.value.fixture) { fixtureBlocked(); return }
        if (_state.value.projectBusy) return
        viewModelScope.launch {
            _state.update { it.copy(projectBusy = true, projectMessage = "正在校验 ZIP 并写入临时工程…") }
            try {
                val settings = graph.settings.current()
                val result = withContext(Dispatchers.IO) { graph.projects.importZip(settings.projectTreeUri, uri, name, conflict) }
                val suffix = if (result.backupRetained) "；旧工程备份未能自动删除，已保留供人工核对" else ""
                _state.update { it.copy(projectBusy = false, projectMessage = "${result.message}：${result.itemCount} 项，${result.bytes} 字节$suffix") }
                refreshProjectRoots()
            } catch (error: CancellationException) { throw error
            } catch (error: Exception) {
                _state.update { it.copy(projectBusy = false, projectMessage = ManagementContract.failure(error)) }
            }
        }
    }

    fun exportProject(uri: Uri, name: String) {
        if (_state.value.fixture) { fixtureBlocked(); return }
        if (_state.value.projectBusy) return
        viewModelScope.launch {
            _state.update { it.copy(projectBusy = true, projectMessage = "正在导出工程 ZIP…") }
            try {
                val settings = graph.settings.current()
                val result = withContext(Dispatchers.IO) { graph.projects.exportZip(settings.projectTreeUri, name, uri) }
                _state.update { it.copy(projectBusy = false, projectMessage = "${result.message}：${result.itemCount} 项，${result.bytes} 字节") }
            } catch (error: CancellationException) { throw error
            } catch (error: Exception) {
                _state.update { it.copy(projectBusy = false, projectMessage = ManagementContract.failure(error)) }
            }
        }
    }

    fun showNotice(value: String) = _state.update { it.copy(message = value.take(2048)) }

    fun exportRecords(uri: Uri, value: JSONObject) {
        if (_state.value.fixture) { fixtureBlocked(); return }
        val frozen = JSONObject(value.toString())
        viewModelScope.launch {
            try {
                dev.agentdock.workbench.data.RecordExporter(getApplication()).write(uri, frozen)
                showNotice("已导出明确选定的记录。")
            } catch (error: CancellationException) { throw error
            } catch (error: Exception) { showError(error) }
        }
    }

    fun exportCallScope(uri: Uri, filter: dev.agentdock.workbench.data.CallFilter) {
        if (_state.value.fixture) { fixtureBlocked(); return }
        viewModelScope.launch {
            try {
                val exporter = dev.agentdock.workbench.data.RecordExporter(getApplication())
                val frozen = exporter.calls(graph.repository.managementClient(), filter)
                exporter.write(uri, frozen)
                showNotice("已导出完整筛选范围内的调用摘要；未预载工具输出。")
            } catch (error: CancellationException) { throw error
            } catch (error: Exception) { showError(error) }
        }
    }

    fun exportBridgeBundle(uri: Uri) {
        if (_state.value.fixture) { fixtureBlocked(); return }
        viewModelScope.launch {
            try {
                withContext(Dispatchers.IO) {
                    val app = getApplication<Application>()
                    val output = app.contentResolver.openOutputStream(uri, "w") ?: error("无法打开导出目标")
                    java.util.zip.ZipOutputStream(output).use { zip ->
                        for (name in listOf("agentdock-workbench", "agentdock_workbench.py", "agentdock-workbench-bootstrap.sh")) {
                            zip.putNextEntry(java.util.zip.ZipEntry(name))
                            app.assets.open(name).use { it.copyTo(zip) }
                            zip.closeEntry()
                        }
                    }
                }
                showNotice("已导出完整桥包。请在外部 Termux 解压并手动运行 bootstrap。")
            } catch (error: CancellationException) { throw error
            } catch (error: Exception) { showError(error) }
        }
    }

    fun exportBundledAsset(uri: Uri, assetName: String) {
        viewModelScope.launch {
            try {
                require(assetName in setOf("agentdock-workbench", "agentdock-workbench-bootstrap.sh"))
                withContext(Dispatchers.IO) {
                    getApplication<Application>().assets.open(assetName).use { input ->
                        getApplication<Application>().contentResolver.openOutputStream(uri, "w")?.use { input.copyTo(it) } ?: error("无法打开导出目标")
                    }
                }
                _state.update { it.copy(message = "已导出 $assetName") }
            } catch (error: CancellationException) { throw error } catch (error: Exception) { showError(error) }
        }
    }

    private fun perform(block: suspend () -> ActionOutcome) {
        if (_state.value.fixture) { fixtureBlocked(); return }
        if (_state.value.actionBusy) return
        _state.update { it.copy(actionBusy = true) }
        viewModelScope.launch {
            try {
                val outcome = block()
                _state.update { it.copy(message = outcome.message) }
                refresh()
            } catch (error: CancellationException) { throw error } catch (error: Exception) { showError(error)
            } finally { _state.update { it.copy(actionBusy = false) } }
        }
    }

    fun setFixtureScenario(value: String) {
        check(BuildConfig.DEBUG && _state.value.fixture)
        require(value in setOf("normal", "empty", "error"))
        saved["fixture_scenario"] = value
        refresh()
    }

    private fun fixtureSnapshot(base: WorkbenchSnapshot): WorkbenchSnapshot = when (saved.get<String>("fixture_scenario")) {
        "empty" -> base.copy(tasks = emptyList(), conversations = emptyList(), calls = emptyList(),
            approvals = emptyList(), insertions = emptyList(), taskPage = dev.agentdock.workbench.data.ResourcePage(total = 0))
        "error" -> base.copy(coreHealth = NodeHealth.Unknown, errors = mapOf("tasks" to "FIXTURE_UNAVAILABLE：测试用断连状态"))
        else -> base
    }

    private fun fixtureBlocked() = _state.update { it.copy(message = "Fixture 模式禁止实际写入与设备操作") }
    private fun requireConversation(): String = _state.value.selectedConversationId.ifBlank { error("请先选择对话") }
    private fun showError(error: Throwable) = _state.update { it.copy(message = ManagementContract.failure(error)) }

    private fun startStreams() {
        activityStream.start(graph.repository::observeActivity) { event ->
                _state.update { it.copy(liveStatus = if (event.event in setOf("disconnected", "stopped")) event.data else "活动流 #${event.id}") }
                if (event.event !in setOf("disconnected", "stopped")) scheduleRefresh()
        }
        callStream.start(graph.repository::observeCalls) { event ->
                _state.update { it.copy(liveStatus = if (event.event in setOf("disconnected", "stopped")) event.data else "调用流 #${event.id}") }
                if (event.event !in setOf("disconnected", "stopped")) scheduleRefresh()
        }
    }

    private fun stopStreams() {
        resources.invalidate()
        payloadGeneration++; payloadJob?.cancel(); detailJob?.cancel()
        refreshGeneration++; refreshJob?.cancel()
        _state.update { it.copy(payload = null, detail = null, snapshot = WorkbenchSnapshot()) }
        activityStream.stop()
        callStream.stop()
        refreshDebounce?.cancel()
    }

    private fun scheduleRefresh() {
        if (refreshDebounce?.isActive == true) return
        refreshDebounce = viewModelScope.launch { delay(1000); if (!_state.value.loading) refresh() }
    }

    private fun restoreQuery(kind: String) = ListQuery(
        view = saved["${kind}_view"] ?: "active", search = saved["${kind}_search"] ?: "",
        status = saved["${kind}_status"] ?: "", workspaceId = saved["${kind}_workspace"] ?: "",
        tag = saved["${kind}_tag"] ?: "", offset = saved["${kind}_offset"] ?: 0
    )

    class Factory(private val application: Application, private val fixture: Boolean) : ViewModelProvider.Factory {
        @Suppress("UNCHECKED_CAST")
        override fun <T : ViewModel> create(modelClass: Class<T>, extras: CreationExtras): T =
            WorkbenchViewModel(application, fixture, extras.createSavedStateHandle()) as T
    }
}
