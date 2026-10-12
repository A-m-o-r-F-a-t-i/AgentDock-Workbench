# Dynamic Core JSON is parsed with org.json; no reflection keep rules are required.

# Shizuku instantiates this Binder by its stable class name and constructors.
-keep class dev.agentdock.workbench.shizuku.PhoneShellService { public <init>(); public <init>(android.content.Context); *; }
-keep class dev.agentdock.workbench.shizuku.NativeCommand { *; }

# Room loads the generated WorkManager database by name and invokes its public
# zero-argument constructor. Release shrinking must retain both (AD-ANDROID-001).
-keep class androidx.work.impl.WorkDatabase_Impl {
    public <init>();
}
