package dev.agentdock.workbench.data

import android.content.ActivityNotFoundException
import android.content.Context
import android.content.Intent
import android.net.Uri
import java.net.URI

/** Launch does not need package-visibility permission or a pre-query. */
object OAuthBrowserLauncher {
    fun open(context: Context, authorizationUrl: URI) {
        val intent = Intent(Intent.ACTION_VIEW, Uri.parse(authorizationUrl.toString()))
            .addFlags(Intent.FLAG_ACTIVITY_NEW_TASK)
        try {
            context.startActivity(intent)
        } catch (error: ActivityNotFoundException) {
            throw IllegalStateException("没有可打开 OAuth 授权页的浏览器，请安装或启用浏览器后重试。", error)
        }
    }
}
