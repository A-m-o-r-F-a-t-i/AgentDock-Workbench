// Package mcpappcompat keeps the Workbench MCP App document stable while the
// shared protocol module advances Bridge transport and capability contracts.
package mcpappcompat

import (
	_ "embed"
	"strings"
)

//go:embed app.html
var appHTMLTemplate string

// HTML renders one shared MCP App document for a known Workbench view.
func HTML(view, title string) string {
	return strings.NewReplacer("{{VIEW}}", view, "{{TITLE}}", title).Replace(appHTMLTemplate)
}
