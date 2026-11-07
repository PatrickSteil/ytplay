package ui

// The keys that need no colon. Everything else is a command (see commands.go).
// The values are Bubble Tea key strings (tea.KeyMsg.String()).
const (
	keySearch  = "/" // the search bar, as ":find" without a name
	keyCommand = ":" // the command line
	keyEnter   = "enter"
	keyEsc     = "esc"
	keyPause   = " "
	keyCtrlC   = "ctrl+c"
	keyDown    = "j"
	keyUp      = "k"
	keySeekEnd = "=" // Shift+0 on a German keyboard

	keyLeft    = "left"  // seek back
	keyRight   = "right" // seek forward
	keyUndo    = "u"     // undo the last queue change
	keyVolUp   = "+"
	keyVolDown = "-"

	keyComplete = "tab" // completes the path in the download popup and a command name in the command line
	keyConfirm  = "y"   // confirm in the yes/no popup
	keyDeny     = "n"   // decline in the yes/no popup (Esc too)

	// Queue editing (modeQueue).
	keyQueueFocus = "tab"  // focus / leave the queue panel
	keyRemove     = "x"    // remove the selected queue entry (like :delete)
	keyShiftUp    = "up"   // move the selected entry up
	keyShiftDown  = "down" // move the selected entry down
	keyMoveUp     = "K"    // like keyShiftUp
	keyMoveDown   = "J"    // like keyShiftDown

	// The command line itself.
	keyBackspace = "backspace"
	keyArrowUp   = "up"   // an earlier command
	keyArrowDown = "down" // a later command
)

// seekPercentForKey maps "0".."9" to 0..90 and keySeekEnd to 100.
func seekPercentForKey(k string) (int, bool) {
	if k == keySeekEnd {
		return 100, true
	}
	if len(k) == 1 && k[0] >= '0' && k[0] <= '9' {
		return int(k[0]-'0') * 10, true
	}
	return 0, false
}
