package config

// Validate runs the same checks Load applies to a config read from disk, so a
// caller (the MCP config tools) can refuse an invalid edit before anything is
// written.
func Validate(cfg *AppConfig) error { return validate(cfg) }
