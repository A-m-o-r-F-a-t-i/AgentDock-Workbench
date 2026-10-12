package dev.agentdock.workbench

import android.app.Application
import androidx.work.Configuration
import dev.agentdock.workbench.data.CredentialStore
import dev.agentdock.workbench.data.SafProjectStore
import dev.agentdock.workbench.data.RemoteOAuthPairingManager
import dev.agentdock.workbench.data.SettingsStore
import dev.agentdock.workbench.data.WorkbenchRepository
import dev.agentdock.workbench.termux.PendingOperationStore
import dev.agentdock.workbench.termux.LocalCorePairingManager
import dev.agentdock.workbench.termux.TermuxCommandDispatcher
import dev.agentdock.workbench.lifecycle.GuardianController
import dev.agentdock.workbench.lifecycle.AndroidGuardianEffects

class WorkbenchApplication : Application(), Configuration.Provider {
    // WorkManager.getInstance(context) initializes only when a scheduler needs
    // it; provider acquisition alone must not open the guardian's database.
    override val workManagerConfiguration: Configuration
        get() = Configuration.Builder().build()

    lateinit var graph: AppGraph
        private set

    override fun onCreate() {
        super.onCreate()
        graph = AppGraph(this)
    }
}

class AppGraph(application: Application) {
    val settings = SettingsStore(application)
    val credentials = CredentialStore(application)
    val phoneExecutor = dev.agentdock.workbench.execution.PhoneExecutor(application, credentials)
    val operations = PendingOperationStore(application)
    val localPairing = LocalCorePairingManager(application, credentials).also {
        it.cleanupExpired(System.currentTimeMillis())
    }
    val remoteOAuth = RemoteOAuthPairingManager(credentials)
    val projects = SafProjectStore(application)
    val repository = WorkbenchRepository(application, settings, credentials)
    val termux = TermuxCommandDispatcher(application, operations, localPairing, settings)
    val guardian = GuardianController(settings::current, settings::update, AndroidGuardianEffects(application))
}
