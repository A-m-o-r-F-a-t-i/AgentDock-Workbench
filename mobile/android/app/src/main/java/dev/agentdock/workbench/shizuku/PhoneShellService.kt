package dev.agentdock.workbench.shizuku

import android.content.Context
import android.os.Process
import org.json.JSONObject

/** Instantiated by Shizuku in its shell/root process, not an Android Service. */
class PhoneShellService : IPhoneShell.Stub {
    private val registry: OwnedShellRegistry
    constructor() : super() { NativeCommand.initialize(); registry = OwnedShellRegistry(Process.myUid()) }
    constructor(context: Context) : super() {
        NativeCommand.initialize(context.applicationInfo.nativeLibraryDir)
        registry = OwnedShellRegistry(Process.myUid())
    }
    override fun control(nodeId: String?, request: String?): String {
        return try {
            require(request != null && request.toByteArray(Charsets.UTF_8).size <= ShellProtocol.MAX_REQUEST)
            registry.control(nodeId.orEmpty(), JSONObject(request)).toString()
        } catch (_: Exception) {
            JSONObject().put("error", "shizuku_control_unconfirmed").toString()
        }
    }
    override fun destroy() { registry.shutdown(); System.exit(0) }
}
