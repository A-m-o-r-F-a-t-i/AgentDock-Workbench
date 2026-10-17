package dev.agentdock.workbench.ui

import androidx.activity.compose.BackHandler
import androidx.compose.foundation.layout.*
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.foundation.verticalScroll
import androidx.compose.material3.*
import androidx.compose.runtime.*
import androidx.compose.runtime.saveable.rememberSaveableStateHolder
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.platform.testTag
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.dp
import dev.agentdock.workbench.BuildConfig
import dev.agentdock.workbench.model.WorkbenchScreen
import kotlinx.coroutines.launch

private val compactDestinations = listOf(WorkbenchScreen.Home, WorkbenchScreen.Tasks, WorkbenchScreen.Activity, WorkbenchScreen.Settings)

@OptIn(ExperimentalMaterial3Api::class)
@Composable
fun WorkbenchApp(state: WorkbenchUiState, viewModel: WorkbenchViewModel) {
    val drawerState = rememberDrawerState(DrawerValue.Closed)
    val snackbar = remember { SnackbarHostState() }
    val scope = rememberCoroutineScope()
    val savedPages = rememberSaveableStateHolder()
    BackHandler(drawerState.isOpen || state.screen != WorkbenchScreen.Home) {
        if (drawerState.isOpen) scope.launch { drawerState.close() } else viewModel.back()
    }
    LaunchedEffect(state.message) {
        if (state.message.isNotBlank()) { snackbar.showSnackbar(state.message); viewModel.clearMessage() }
    }
    ModalNavigationDrawer(drawerState = drawerState, drawerContent = {
        ModalDrawerSheet(modifier = Modifier.widthIn(max = 320.dp), drawerContainerColor = MaterialTheme.colorScheme.surface) {
            Column(Modifier.fillMaxHeight().verticalScroll(rememberScrollState()).padding(horizontal = 12.dp)) {
                Text("ACTOVO", Modifier.padding(start = 16.dp, top = 24.dp, bottom = 20.dp).testTag("drawer-title"),
                    style = MaterialTheme.typography.headlineSmall, fontWeight = FontWeight.SemiBold)
                WorkbenchScreen.entries.groupBy { it.section }.forEach { (section, screens) ->
                    Text(section, Modifier.padding(start = 16.dp, top = 16.dp, bottom = 6.dp),
                        style = MaterialTheme.typography.labelMedium, color = MaterialTheme.colorScheme.onSurfaceVariant)
                    screens.forEach { screen ->
                        NavigationDrawerItem(
                            label = { Text(screen.title, maxLines = 1, overflow = TextOverflow.Ellipsis) },
                            icon = { Icon(WorkbenchIcons.forRoute(screen.route), contentDescription = null) },
                            selected = state.screen == screen,
                            shape = RoundedCornerShape(16.dp),
                            modifier = Modifier.padding(vertical = 2.dp).testTag("nav-${screen.route}"),
                            onClick = { viewModel.navigate(screen); scope.launch { drawerState.close() } }
                        )
                    }
                }
                HorizontalDivider(Modifier.padding(top = 20.dp, bottom = 12.dp))
                Text("ACTOVO ${BuildConfig.PRODUCT_VERSION}", Modifier.padding(horizontal = 16.dp, vertical = 8.dp),
                    style = MaterialTheme.typography.bodySmall, color = MaterialTheme.colorScheme.onSurfaceVariant)
                if (state.fixture) Text("测试数据", Modifier.padding(horizontal = 16.dp, vertical = 8.dp), style = MaterialTheme.typography.labelMedium)
                Spacer(Modifier.height(12.dp))
            }
        }
    }) {
        BoxWithConstraints(Modifier.fillMaxSize()) {
            val compact = maxWidth < 700.dp
            Scaffold(
                containerColor = MaterialTheme.colorScheme.background,
                topBar = {
                    TopAppBar(
                        modifier = Modifier.testTag("workbench-appbar"),
                        title = { Text(state.screen.title, maxLines = 1, overflow = TextOverflow.Ellipsis) },
                        navigationIcon = {
                            IconButton(onClick = { scope.launch { drawerState.open() } }, modifier = Modifier.testTag("open-navigation")) {
                                Icon(WorkbenchIcons.Menu, contentDescription = "打开菜单")
                            }
                        },
                        actions = {
                            IconButton(onClick = viewModel::refresh, enabled = !state.loading, modifier = Modifier.testTag("refresh")) {
                                Icon(WorkbenchIcons.Refresh, contentDescription = "刷新")
                            }
                        },
                        colors = TopAppBarDefaults.topAppBarColors(containerColor = MaterialTheme.colorScheme.background)
                    )
                },
                snackbarHost = { SnackbarHost(snackbar) },
                bottomBar = {
                    if (compact) NavigationBar(containerColor = MaterialTheme.colorScheme.surface, tonalElevation = 0.dp) {
                        compactDestinations.forEach { screen ->
                            NavigationBarItem(selected = state.screen == screen, onClick = { viewModel.navigate(screen) },
                                icon = { Icon(WorkbenchIcons.forRoute(screen.route), contentDescription = null) },
                                label = { Text(screen.title, maxLines = 1) }, modifier = Modifier.testTag("bottom-${screen.route}"))
                        }
                    }
                }
            ) { padding ->
                // Apply the Scaffold insets exactly once to the whole body. The
                // wide navigation rail and the page share the same safe region.
                Box(Modifier.fillMaxSize().padding(padding).consumeWindowInsets(padding).testTag("workbench-content")) {
                    Row(Modifier.fillMaxSize()) {
                        if (!compact) NavigationRail(windowInsets = WindowInsets(0, 0, 0, 0),
                            containerColor = MaterialTheme.colorScheme.background, modifier = Modifier.testTag("workbench-rail")) {
                            Spacer(Modifier.height(12.dp))
                            compactDestinations.forEach { screen ->
                                NavigationRailItem(selected = state.screen == screen, onClick = { viewModel.navigate(screen) },
                                    icon = { Icon(WorkbenchIcons.forRoute(screen.route), contentDescription = null) },
                                    label = { Text(screen.title) }, modifier = Modifier.testTag("rail-${screen.route}"))
                                Spacer(Modifier.height(8.dp))
                            }
                        }
                        Box(Modifier.weight(1f).fillMaxHeight(), contentAlignment = Alignment.TopCenter) {
                            savedPages.SaveableStateProvider(state.screen.route) {
                                WorkbenchPage(state, viewModel,
                                    modifier = Modifier.widthIn(max = 1120.dp).fillMaxSize().testTag("screen-${state.screen.route}"))
                            }
                        }
                    }
                    if (state.loading) LinearProgressIndicator(Modifier.fillMaxWidth())
                }
            }
        }
    }
}
