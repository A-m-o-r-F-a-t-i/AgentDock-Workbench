# 原生窗口与系统栏主题

## Windows

`DesktopTheme` 在同一次主题应用中更新WPF资源和DWM非客户区。控制面板与活动中心在HWND初始化时应用，后续辅助窗口由统一Loaded类处理器补齐。已经打开的窗口即时响应浅色、深色、跟随系统切换，关闭时解绑窗口事件，退出时清理跟踪集合。

标题栏使用系统原生按钮、菜单、缩放与DPI行为，不替换为无边框自绘窗口。DWM深色模式使用属性20；Windows 11起额外把现有AppBackground/PrimaryText映射到标题背景和文字。高对比度恢复系统默认标题颜色。旧系统不接受的DWM属性安全保留原生外观，不阻断启动或主题切换。

## Android

Compose内容与系统状态栏、导航栏共用同一个解析后的主题。手动选择浅色或深色覆盖系统模式，跟随系统继续响应系统设置变化。API 26–34设置共享Material surface背景，API 35以上保留系统edge-to-edge与导航对比度规则，只同步前景图标及窗口底色。保留现有insets处理，不改变页面布局或节点运行状态。夜间资源同步启动阶段的系统栏模式。

## macOS与Linux

macOS已经通过NSApplication.appearance对现有及后建原生窗口应用主题，继续复用该实现，新增NSWindow/NSPanel回归覆盖浅色、深色、跟随系统。Linux保持CLI，由终端负责窗口外观。

## 验证

Windows回归在Actions创建未显示的真实HWND，读取DWM深色模式并检查标题颜色设置回执，验证初始与切换状态、后建窗口、事件解绑和高对比度策略。标题背景和文字颜色遵循DwmSetWindowAttribute设置接口，不使用不支持的getter反查。macOS原生测试验证窗口继承。Android包含主题解析单测和无Core/Termux依赖的Compose系统栏仪器测试。
