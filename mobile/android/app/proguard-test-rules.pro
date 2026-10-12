# Instrumentation-only dependency: Error Prone annotations refer to the JDK
# compiler enum. Android has no javax.lang.model; this is not application code.
# Keep the production APK's R8 rules and missing-class checks unchanged.
-dontwarn javax.lang.model.element.Modifier
