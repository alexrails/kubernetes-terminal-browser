package actions

import "kubernetes-terminal-browser/internal/config"

// Shell selects the user's sh action when present. Exit status never selects
// another executable automatically.
func Shell(c config.Config) config.Action {
	for _, a := range c.Actions {
		if a.ID == "sh" {
			return a
		}
	}
	return config.Action{ID: "sh", Label: "Shell", Argv: []string{"/bin/sh"}, Stdin: true, TTY: true}
}
