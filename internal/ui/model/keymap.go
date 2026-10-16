package model

// CommandKind tells who handles a command.
type CommandKind int

const (
	// CommandNav is a navigation action, handled by Navigate.
	CommandNav CommandKind = iota + 1
	// CommandRedraw repaints the whole screen.
	CommandRedraw
	CommandQuit
	// CommandClose asks to close the selected session (handled by the app).
	CommandClose
)

// Command is what a key does: a navigation action, or one handled by the app itself.
type Command struct {
	Kind CommandKind
	Nav  Action
}

func nav(t ActionType, dir Direction) (Command, bool) {
	return Command{Kind: CommandNav, Nav: Action{Type: t, Dir: dir}}, true
}

// KeyToCommand maps a key, named as in Bubble Tea's KeyMsg.String ("up", "enter", "ctrl+c",
// "k"...), to its command; ok is false for keys with no action.
func KeyToCommand(key string) (cmd Command, ok bool) {
	switch key {
	case "ctrl+c", "q":
		return Command{Kind: CommandQuit}, true
	case "ctrl+l":
		return Command{Kind: CommandRedraw}, true
	case "up", "k":
		return nav(ActionMove, DirUp)
	case "down", "j":
		return nav(ActionMove, DirDown)
	case "left", "h":
		return nav(ActionMove, DirLeft)
	case "right", "l":
		return nav(ActionMove, DirRight)
	case "enter":
		return nav(ActionOpen, "")
	case "esc", "backspace", "delete", "ctrl+h":
		return nav(ActionBack, "")
	case "tab", "shift+tab":
		return nav(ActionTab, "")
	case "?":
		return nav(ActionToggleHelp, "")
	case "p":
		return nav(ActionCycleProfile, "")
	case "x":
		return Command{Kind: CommandClose}, true
	}
	return Command{}, false
}
