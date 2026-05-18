package agent

type AdapterConfig struct {
	Command string
	Args    []string
	Prompt  string
	Env     map[string]string
}

func NewCodexAdapter(cfg AdapterConfig) ProcessAdapter {
	command := cfg.Command
	if command == "" {
		command = "codex"
	}
	mode := PromptMode(cfg.Prompt)
	if mode == "" {
		mode = PromptStdin
	}
	return ProcessAdapter{
		AdapterName: "codex",
		Command:     command,
		Args:        cfg.Args,
		PromptMode:  mode,
		Env:         cfg.Env,
	}
}
