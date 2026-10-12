package dev.agentdock.workbench.ui

import androidx.compose.ui.graphics.Color
import androidx.compose.ui.graphics.StrokeCap
import androidx.compose.ui.graphics.StrokeJoin
import androidx.compose.ui.graphics.SolidColor
import androidx.compose.ui.graphics.vector.ImageVector
import androidx.compose.ui.graphics.vector.PathBuilder
import androidx.compose.ui.graphics.vector.path
import androidx.compose.ui.unit.dp

/** Small original stroke icons; no font glyphs or external artwork. */
internal object WorkbenchIcons {
    private fun icon(name: String, draw: PathBuilder.() -> Unit): ImageVector =
        ImageVector.Builder(name, 24.dp, 24.dp, 24f, 24f).apply {
            path(fill = null, stroke = SolidColor(Color.Black), strokeLineWidth = 1.8f,
                strokeLineCap = StrokeCap.Round, strokeLineJoin = StrokeJoin.Round, pathBuilder = draw)
        }.build()
    private fun PathBuilder.box(left: Float, top: Float, right: Float, bottom: Float) {
        moveTo(left, top); lineTo(right, top); lineTo(right, bottom); lineTo(left, bottom); close()
    }
    val Menu = icon("Menu") { moveTo(4f,6f); lineTo(20f,6f); moveTo(4f,12f); lineTo(20f,12f); moveTo(4f,18f); lineTo(20f,18f) }
    val Refresh = icon("Refresh") { moveTo(20f,9f); curveTo(18f,3f,8f,2f,5f,8f); moveTo(4f,15f); curveTo(6f,21f,16f,22f,19f,16f); moveTo(20f,4f); lineTo(20f,9f); lineTo(15f,9f); moveTo(4f,20f); lineTo(4f,15f); lineTo(9f,15f) }
    val Home = icon("Home") { moveTo(3f,11f); lineTo(12f,4f); lineTo(21f,11f); moveTo(5f,10f); lineTo(5f,21f); lineTo(10f,21f); lineTo(10f,15f); lineTo(14f,15f); lineTo(14f,21f); lineTo(19f,21f); lineTo(19f,10f) }
    val Tasks = icon("Tasks") { box(5f,4f,20f,21f); moveTo(9f,9f); lineTo(11f,11f); lineTo(15f,7f); moveTo(9f,16f); lineTo(16f,16f) }
    val Activity = icon("Activity") { moveTo(3f,12f); lineTo(7f,12f); lineTo(10f,5f); lineTo(14f,19f); lineTo(17f,12f); lineTo(21f,12f) }
    val Settings = icon("Settings") { moveTo(4f,7f); lineTo(8f,7f); moveTo(12f,7f); lineTo(20f,7f); box(8f,5f,12f,9f); moveTo(4f,17f); lineTo(13f,17f); moveTo(17f,17f); lineTo(20f,17f); box(13f,15f,17f,19f) }
    val Folder = icon("Folder") { moveTo(3f,7f); lineTo(3f,4f); lineTo(10f,4f); lineTo(13f,7f); lineTo(21f,7f); lineTo(21f,20f); lineTo(3f,20f); close() }
    val Chat = icon("Chat") { moveTo(4f,4f); lineTo(20f,4f); lineTo(20f,17f); lineTo(10f,17f); lineTo(4f,21f); close(); moveTo(8f,9f); lineTo(16f,9f); moveTo(8f,13f); lineTo(13f,13f) }
    val Shield = icon("Shield") { moveTo(12f,3f); lineTo(20f,6f); lineTo(19f,14f); curveTo(18f,18f,14f,20f,12f,21f); curveTo(10f,20f,6f,18f,5f,14f); lineTo(4f,6f); close(); moveTo(8f,11f); lineTo(11f,14f); lineTo(16f,9f) }
    val Tools = icon("Tools") { box(4f,4f,10f,10f); box(14f,4f,20f,10f); box(4f,14f,10f,20f); moveTo(17f,14f); lineTo(17f,20f); moveTo(14f,17f); lineTo(20f,17f) }
    val Connection = icon("Connection") { box(3f,6f,9f,18f); box(15f,6f,21f,18f); moveTo(9f,12f); lineTo(15f,12f) }
    val Download = icon("Download") { moveTo(12f,3f); lineTo(12f,15f); moveTo(7f,10f); lineTo(12f,15f); lineTo(17f,10f); moveTo(4f,16f); lineTo(4f,21f); lineTo(20f,21f); lineTo(20f,16f) }
    val Document = icon("Document") { moveTo(5f,3f); lineTo(15f,3f); lineTo(20f,8f); lineTo(20f,21f); lineTo(5f,21f); close(); moveTo(14f,3f); lineTo(14f,9f); lineTo(20f,9f); moveTo(9f,13f); lineTo(16f,13f); moveTo(9f,17f); lineTo(16f,17f) }
    fun forRoute(route: String): ImageVector = when (route) {
        "home" -> Home; "tasks" -> Tasks; "activity" -> Activity; "settings" -> Settings
        "workspaces", "projects" -> Folder; "conversations", "insert" -> Chat
        "approvals", "permissions" -> Shield; "skills", "plugins" -> Tools
        "connections" -> Connection; "install" -> Download; else -> Document
    }
}
