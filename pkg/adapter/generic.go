package adapter

// GenericAdapter is a fallback for any CLI agent without a specific adapter.
// Uses screen scraping via tmux capture-pane for output observation.
type GenericAdapter struct {
	name      string
	binary    string
	connector string // CASS connector name, if any
	extraArgs []string
}

// NewGeneric creates an adapter for an arbitrary CLI agent.
func NewGeneric(name, binary, cassConnector string, defaultArgs ...string) *GenericAdapter {
	return &GenericAdapter{
		name:      name,
		binary:    binary,
		connector: cassConnector,
		extraArgs: defaultArgs,
	}
}

func (a *GenericAdapter) Name() string { return a.name }

func (a *GenericAdapter) SpawnCmd(workDir string, cfg Config) (string, []string) {
	args := make([]string, len(a.extraArgs))
	copy(args, a.extraArgs)
	args = append(args, cfg.ExtraArgs...)
	return a.binary, args
}

func (a *GenericAdapter) ResumeCmd(_ string, _ string, _ Config) (string, []string) {
	return "", nil
}

func (a *GenericAdapter) FormatPrompt(prompt string) string {
	return prompt
}

func (a *GenericAdapter) SessionDir() string    { return "" }
func (a *GenericAdapter) CassConnector() string { return a.connector }
func (a *GenericAdapter) SupportsResume() bool  { return false }
