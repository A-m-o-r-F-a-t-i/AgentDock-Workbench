# Dynamic Core JSON is parsed with org.json; no reflection keep rules are required.

# Shizuku instantiates this Binder by its stable class name and constructors.
-keep class dev.agentdock.workbench.shizuku.PhoneShellService { public <init>(); public <init>(android.content.Context); *; }
-keep class dev.agentdock.workbench.shizuku.NativeCommand { *; }
