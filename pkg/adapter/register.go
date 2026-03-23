package adapter

func init() {
	// Register well-known agents from CASS connectors using the generic adapter.
	// Claude Code and Codex register themselves via their own init().
	Register(NewGeneric("gemini", "gemini", "gemini"))
	Register(NewGeneric("amp", "amp", "amp"))
	Register(NewGeneric("aider", "aider", "aider"))
	Register(NewGeneric("cline", "cline", "cline"))
	Register(NewGeneric("cursor", "cursor", "cursor"))
	Register(NewGeneric("copilot", "copilot", "copilot"))
}
