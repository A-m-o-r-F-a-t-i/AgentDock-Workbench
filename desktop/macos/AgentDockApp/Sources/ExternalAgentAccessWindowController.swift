import AppKit

final class ExternalAgentAccessWindowController: NSWindowController, NSWindowDelegate {
    private var serviceStatus = ServiceStatus.missing

    private let endpointSourcePopup = NSPopUpButton(frame: .zero, pullsDown: false)
    private let clientPopup = NSPopUpButton(frame: .zero, pullsDown: false)
    private let endpointField = NSTextField(labelWithString: "")
    private let configurationTextView = NSTextView(frame: .zero)
    private let copyEndpointButton = NSButton(title: L10n.text("Copy endpoint"), target: nil, action: nil)
    private let copyConfigurationButton = NSButton(title: L10n.text("Copy configuration"), target: nil, action: nil)
    private let statusLabel = NSTextField(labelWithString: "")

    init() {
        let window = NSWindow(
            contentRect: NSRect(x: 0, y: 0, width: 760, height: 590),
            styleMask: [.titled, .closable, .resizable],
            backing: .buffered,
            defer: false
        )
        window.title = L10n.text("External agent access")
        window.minSize = NSSize(width: 640, height: 500)
        window.isReleasedWhenClosed = false
        super.init(window: window)
        window.delegate = self
        configureWindow()
    }

    @available(*, unavailable)
    required init?(coder: NSCoder) {
        fatalError("init(coder:) has not been implemented")
    }

    func present(status: ServiceStatus) {
        update(status: status)
        showWindow(nil)
        window?.center()
        window?.makeKeyAndOrderFront(nil)
        NSApp.activate(ignoringOtherApps: true)
    }

    func update(status: ServiceStatus) {
        serviceStatus = status
        refreshConfiguration()
    }

    private func configureWindow() {
        endpointSourcePopup.addItems(withTitles: [
            L10n.text("Local MCP · this Mac"),
            L10n.text("Public MCP · remote agents"),
        ])
        endpointSourcePopup.target = self
        endpointSourcePopup.action = #selector(selectionChanged)

        clientPopup.addItems(withTitles: [
            L10n.text("Codex"),
            L10n.text("Claude Code"),
            L10n.text("Generic MCP JSON"),
            L10n.text("Agent setup prompt"),
        ])
        clientPopup.target = self
        clientPopup.action = #selector(selectionChanged)

        endpointField.isSelectable = true
        endpointField.font = NSFont.monospacedSystemFont(ofSize: NSFont.systemFontSize, weight: .regular)
        endpointField.lineBreakMode = .byTruncatingMiddle

        configurationTextView.isEditable = false
        configurationTextView.isSelectable = true
        configurationTextView.isRichText = false
        configurationTextView.usesFindPanel = true
        configurationTextView.font = NSFont.monospacedSystemFont(ofSize: 12, weight: .regular)
        configurationTextView.textContainerInset = NSSize(width: 8, height: 8)
        configurationTextView.frame = NSRect(x: 0, y: 0, width: 700, height: 280)
        configurationTextView.minSize = NSSize(width: 0, height: 0)
        configurationTextView.maxSize = NSSize(
            width: CGFloat.greatestFiniteMagnitude,
            height: CGFloat.greatestFiniteMagnitude
        )
        configurationTextView.isVerticallyResizable = true
        configurationTextView.isHorizontallyResizable = true
        configurationTextView.autoresizingMask = [.width]
        configurationTextView.textContainer?.widthTracksTextView = false
        configurationTextView.textContainer?.containerSize = NSSize(
            width: CGFloat.greatestFiniteMagnitude,
            height: CGFloat.greatestFiniteMagnitude
        )
        configurationTextView.isAutomaticQuoteSubstitutionEnabled = false
        configurationTextView.isAutomaticDashSubstitutionEnabled = false
        configurationTextView.isAutomaticTextReplacementEnabled = false
        configurationTextView.isAutomaticSpellingCorrectionEnabled = false
        configurationTextView.string = ""

        copyEndpointButton.target = self
        copyEndpointButton.action = #selector(copyEndpoint)
        copyConfigurationButton.target = self
        copyConfigurationButton.action = #selector(copyConfiguration)
        let refreshButton = NSButton(title: L10n.text("Refresh"), target: self, action: #selector(refreshPressed))

        let title = NSTextField(labelWithString: L10n.text("External agent access"))
        title.font = NSFont.systemFont(ofSize: 20, weight: .semibold)
        let description = wrappedLabel(L10n.text("Connect Codex, Claude Code, or another Streamable HTTP MCP client to AgentDock."))

        let endpointRow = horizontalStack([endpointField, copyEndpointButton])
        endpointField.setContentHuggingPriority(.defaultLow, for: .horizontal)
        endpointField.setContentCompressionResistancePriority(.defaultLow, for: .horizontal)

        let form = NSGridView(views: [
            [NSTextField(labelWithString: L10n.text("Connection target")), endpointSourcePopup],
            [NSTextField(labelWithString: L10n.text("MCP endpoint")), endpointRow],
            [NSTextField(labelWithString: L10n.text("Agent client")), clientPopup],
        ])
        form.rowSpacing = 10
        form.columnSpacing = 14
        form.column(at: 0).xPlacement = .trailing
        form.column(at: 1).xPlacement = .fill

        let configurationLabel = NSTextField(labelWithString: L10n.text("Configuration"))
        configurationLabel.font = NSFont.systemFont(ofSize: NSFont.systemFontSize, weight: .semibold)

        let configurationScroll = NSScrollView()
        configurationScroll.borderType = .bezelBorder
        configurationScroll.hasVerticalScroller = true
        configurationScroll.hasHorizontalScroller = true
        configurationScroll.autohidesScrollers = true
        configurationScroll.documentView = configurationTextView

        let actionSpacer = NSView()
        actionSpacer.setContentHuggingPriority(.defaultLow, for: .horizontal)
        actionSpacer.setContentCompressionResistancePriority(.defaultLow, for: .horizontal)
        let actionRow = horizontalStack([copyConfigurationButton, refreshButton, actionSpacer])
        let securityNote = wrappedLabel(L10n.text("Generated configuration contains no actual credential. OAuth uses client discovery; Bearer configurations reference AGENTDOCK_MCP_BEARER. AgentDock permissions, approvals, workspace boundaries, and activity records continue to apply."))
        securityNote.textColor = .secondaryLabelColor
        statusLabel.textColor = .secondaryLabelColor
        statusLabel.lineBreakMode = .byWordWrapping
        statusLabel.maximumNumberOfLines = 2

        let root = NSStackView(views: [
            title,
            description,
            form,
            configurationLabel,
            configurationScroll,
            actionRow,
            securityNote,
            statusLabel,
        ])
        root.orientation = .vertical
        root.alignment = .leading
        root.spacing = 12
        root.translatesAutoresizingMaskIntoConstraints = false

        guard let contentView = window?.contentView else { return }
        contentView.addSubview(root)
        for view in [description, form, configurationScroll, actionRow, securityNote, statusLabel] {
            view.translatesAutoresizingMaskIntoConstraints = false
            view.widthAnchor.constraint(equalTo: root.widthAnchor).isActive = true
        }
        NSLayoutConstraint.activate([
            root.leadingAnchor.constraint(equalTo: contentView.leadingAnchor, constant: 20),
            root.trailingAnchor.constraint(equalTo: contentView.trailingAnchor, constant: -20),
            root.topAnchor.constraint(equalTo: contentView.topAnchor, constant: 20),
            root.bottomAnchor.constraint(equalTo: contentView.bottomAnchor, constant: -20),
            configurationScroll.heightAnchor.constraint(greaterThanOrEqualToConstant: 260),
        ])

        refreshConfiguration()
    }

    @objc private func selectionChanged() {
        refreshConfiguration()
    }

    @objc private func refreshPressed() {
        refreshConfiguration()
    }

    @objc private func copyEndpoint() {
        copy(endpointField.stringValue, successMessage: L10n.text("MCP endpoint copied."))
    }

    @objc private func copyConfiguration() {
        copy(configurationTextView.string, successMessage: L10n.text("Agent configuration copied."))
    }

    private func refreshConfiguration() {
        let localEndpoint = ExternalAgentAccess.normalizedEndpoint(serviceStatus.configuration?.localMCPURL?.absoluteString)
        let publicEndpoint = ExternalAgentAccess.normalizedEndpoint(serviceStatus.configuration?.publicMCPURL?.absoluteString)
        endpointSourcePopup.item(at: ExternalAgentEndpointSource.publicAccess.rawValue)?.isEnabled = publicEndpoint != nil

        if endpointSourcePopup.indexOfSelectedItem == ExternalAgentEndpointSource.publicAccess.rawValue,
           publicEndpoint == nil {
            endpointSourcePopup.selectItem(at: ExternalAgentEndpointSource.local.rawValue)
        }

        let endpoint = endpointSourcePopup.indexOfSelectedItem == ExternalAgentEndpointSource.publicAccess.rawValue
            ? publicEndpoint
            : localEndpoint
        guard let endpoint else {
            endpointField.stringValue = L10n.text("Not configured")
            configurationTextView.string = L10n.text("A valid AgentDock HTTP(S) MCP endpoint ending in /mcp is required.")
            copyEndpointButton.isEnabled = false
            copyConfigurationButton.isEnabled = false
            setStatus(L10n.text("The AgentDock MCP endpoint is unavailable. Start Core or refresh the runtime status."), isError: true)
            return
        }

        endpointField.stringValue = endpoint
        let client = ExternalAgentClientKind(rawValue: clientPopup.indexOfSelectedItem) ?? .codex
        let runtimeConfiguration = serviceStatus.configuration
        let authMode = ExternalAgentAccess.authMode(
            oauthEnabled: runtimeConfiguration?.oauthEnabled == true,
            authRequired: runtimeConfiguration?.authRequired == true
        )
        let authenticationInstruction: String
        switch authMode {
        case .oauth:
            authenticationInstruction = L10n.text("Use the server OAuth discovery flow and complete interactive authorization when requested.")
        case .bearer:
            authenticationInstruction = L10n.text("Read the existing AgentDock bearer credential from a secret store or the AGENTDOCK_MCP_BEARER environment variable; never embed it in prompts or source files.")
        case .none:
            authenticationInstruction = L10n.text("No authentication is required for this loopback endpoint.")
        }
        do {
            configurationTextView.string = try ExternalAgentAccess.configuration(
                for: client,
                endpoint: endpoint,
                authMode: authMode,
                agentSetupPromptTemplate: L10n.text("Connect to ACTOVO through the Streamable HTTP MCP server at %@. Register it as \"agentdock\". %@ After connecting, call agentdock_context once, then reuse the returned context while context_diagnostics.complete=true. Respect ACTOVO workspace, approval, and permission boundaries. Never copy access credentials into prompts, logs, chat history, or project files."),
                authenticationInstruction: authenticationInstruction)
            copyEndpointButton.isEnabled = true
            copyConfigurationButton.isEnabled = true
            setStatus(L10n.text("Ready · the generated configuration matches the current AgentDock authentication and permission controls."), isError: false)
        } catch {
            configurationTextView.string = L10n.text("A valid AgentDock HTTP(S) MCP endpoint ending in /mcp is required.")
            copyEndpointButton.isEnabled = false
            copyConfigurationButton.isEnabled = false
            setStatus(L10n.text("The AgentDock MCP endpoint is unavailable. Start Core or refresh the runtime status."), isError: true)
        }
    }

    private func copy(_ value: String, successMessage: String) {
        guard !value.isEmpty else { return }
        let pasteboard = NSPasteboard.general
        pasteboard.clearContents()
        pasteboard.setString(value, forType: .string)
        setStatus(successMessage, isError: false)
    }

    private func setStatus(_ text: String, isError: Bool) {
        statusLabel.stringValue = text
        statusLabel.textColor = isError ? .systemRed : .secondaryLabelColor
    }

    private func wrappedLabel(_ text: String) -> NSTextField {
        let label = NSTextField(wrappingLabelWithString: text)
        label.maximumNumberOfLines = 0
        return label
    }

    private func horizontalStack(_ views: [NSView]) -> NSStackView {
        let stack = NSStackView(views: views)
        stack.orientation = .horizontal
        stack.alignment = .centerY
        stack.spacing = 8
        return stack
    }
}
