package cmd

func tmuxSubcommand(args []string) string {
	if len(args) >= 4 && args[0] == "tmux" && (args[1] == "-L" || args[1] == "-S") {
		return args[3]
	}
	return ""
}
