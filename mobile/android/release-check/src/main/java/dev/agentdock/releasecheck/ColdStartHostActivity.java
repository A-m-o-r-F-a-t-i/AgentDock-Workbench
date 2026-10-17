package dev.agentdock.releasecheck;

import android.app.Activity;
import android.os.Bundle;
import android.widget.TextView;

/** Test-APK-only foreground holder; keeps the launcher out of cold-start cycles. */
public final class ColdStartHostActivity extends Activity {
    @Override public void onCreate(Bundle state) {
        super.onCreate(state);
        TextView label = new TextView(this);
        label.setText("AgentDock release startup test");
        setContentView(label);
    }
}
