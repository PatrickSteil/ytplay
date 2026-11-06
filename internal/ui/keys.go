package ui

// All key bindings live here so they are easy to change. The values are
// Bubble Tea key strings (tea.KeyMsg.String()).
const (
	keySearch  = "/"
	keyEnter   = "enter"
	keyEsc     = "esc"
	keyQueue   = "q"
	keySkip    = "s"
	keyPause   = " "
	keyQuit    = "Q"
	keyCtrlC   = "ctrl+c"
	keyDown    = "j"
	keyUp      = "k"
	keySeekEnd = "=" // Shift+0 on a German keyboard

	keyDownload   = "d"   // save a song to a file (popup asks where)
	keyComplete   = "tab" // complete the path in the download popup
	keyRadio      = "r"   // add songs similar to the queue
	keyClearQueue = "X"   // clear the queue (asks first)
	keyConfirm    = "y"   // confirm in the popup
	keyDeny       = "n"   // decline in the popup (Esc too)

	// Queue editing (modeQueue).
	keyQueueFocus = "tab"  // focus / leave the queue panel
	keyRemove     = "x"    // remove the selected queue entry
	keyShiftUp    = "up"   // move the selected entry up
	keyShiftDown  = "down" // move the selected entry down
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
