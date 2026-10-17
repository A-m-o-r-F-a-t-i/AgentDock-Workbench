package app

// OnMCPCatalogChanged bridges a published provider directory to the host's
// standard tools/list_changed notification. It does not execute provider tools.
func (r *Runtime) OnMCPCatalogChanged(changed func()) {
	if r != nil && r.capabilityManager != nil {
		r.capabilityManager.SetCatalogChangeObserver(changed)
	}
}
