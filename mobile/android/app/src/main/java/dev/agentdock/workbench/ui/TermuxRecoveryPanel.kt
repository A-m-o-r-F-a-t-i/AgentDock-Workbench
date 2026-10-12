package dev.agentdock.workbench.ui

import android.content.Intent
import android.net.Uri
import android.provider.Settings
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.FlowRow
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
import androidx.compose.material3.Card
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.OutlinedButton
import androidx.compose.material3.Text
import androidx.compose.material3.TextButton
import androidx.compose.runtime.Composable
import androidx.compose.runtime.DisposableEffect
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableLongStateOf
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.setValue
import androidx.compose.ui.Modifier
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.platform.testTag
import androidx.compose.ui.unit.dp
import androidx.lifecycle.Lifecycle
import androidx.lifecycle.LifecycleEventObserver
import androidx.lifecycle.compose.LocalLifecycleOwner
import dev.agentdock.workbench.WorkbenchApplication
import dev.agentdock.workbench.model.ActionOutcome
import dev.agentdock.workbench.termux.TermuxContract
import dev.agentdock.workbench.termux.TermuxRecoveryPolicy
import kotlinx.coroutines.delay

/** Opening settings or returning from Termux never dispatches a command. */
@Composable
internal fun TermuxRecoveryPanel(state: WorkbenchUiState, model: WorkbenchViewModel) {
    val context = LocalContext.current
    val lifecycle = LocalLifecycleOwner.current.lifecycle
    fun readAvailability(): ActionOutcome = if (state.fixture) ActionOutcome(true, "fixture", "") else
        (context.applicationContext as WorkbenchApplication).graph.termux.availability()
    var availability by remember(state.fixture) { mutableStateOf(readAvailability()) }
    var now by remember { mutableLongStateOf(System.currentTimeMillis()) }
    DisposableEffect(lifecycle, state.fixture) {
        val observer = LifecycleEventObserver { _, event ->
            if (event == Lifecycle.Event.ON_RESUME) {
                availability = readAvailability()
                now = System.currentTimeMillis()
            }
        }
        lifecycle.addObserver(observer)
        onDispose { lifecycle.removeObserver(observer) }
    }
    val latest = state.operations.maxByOrNull { it.createdAtEpochMs }
    LaunchedEffect(latest?.operationId, latest?.phase) {
        now = System.currentTimeMillis()
        delay(TermuxRecoveryPolicy.SLOW_RESPONSE_MS)
        now = System.currentTimeMillis()
    }
    val advice = TermuxRecoveryPolicy.advise(availability, latest, now)
    Card(Modifier.fillMaxWidth().testTag("termux-recovery")) {
        Column(Modifier.padding(16.dp), verticalArrangement = Arrangement.spacedBy(8.dp)) {
            Text(advice.title, style = MaterialTheme.typography.titleMedium)
            Text(advice.detail, style = MaterialTheme.typography.bodyMedium)
            FlowRow(horizontalArrangement = Arrangement.spacedBy(8.dp)) {
                TextButton(onClick = {
                    val intent = context.packageManager.getLaunchIntentForPackage(TermuxContract.PACKAGE)
                    if (intent == null) model.showNotice("请先安装官方 Termux。")
                    else runCatching { context.startActivity(intent) }.onFailure { model.showNotice("请从桌面打开 Termux。") }
                }, modifier = Modifier.testTag("termux-open-recovery")) { Text("打开 Termux") }
                OutlinedButton(onClick = { model.runTermux("probe") }, enabled = advice.allowProbe && !state.actionBusy,
                    modifier = Modifier.testTag("termux-reprobe")) { Text("重新检测") }
                TextButton(onClick = {
                    runCatching { context.startActivity(Intent(Settings.ACTION_APPLICATION_DETAILS_SETTINGS,
                        Uri.parse("package:${TermuxContract.PACKAGE}"))) }.onFailure { model.showNotice("请在系统设置中打开 Termux 的应用信息。") }
                }) { Text("Termux 系统设置") }
            }
        }
    }
}
