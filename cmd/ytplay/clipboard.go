package main

import (
	"bytes"
	"fmt"
	"io"
	"os/exec"

	"github.com/charmbracelet/x/ansi"
)

// clipboardTools are the programs that put their input on the clipboard, in the
// order they are tried: Wayland, X11, macOS.
var clipboardTools = [][]string{
	{"wl-copy"},
	{"xclip", "-selection", "clipboard"},
	{"xsel", "--clipboard", "--input"},
	{"pbcopy"},
}

// copyToClipboard puts text on the system clipboard with the first tool that is
// installed and works. With none it asks the terminal to do it (the OSC 52
// escape, which many terminals honour, also over ssh), written to term.
func copyToClipboard(text string, term io.Writer) error {
	for _, tool := range clipboardTools {
		path, err := exec.LookPath(tool[0])
		if err != nil {
			continue
		}
		cmd := exec.Command(path, tool[1:]...)
		cmd.Stdin = bytes.NewBufferString(text)
		if err := cmd.Run(); err == nil {
			return nil
		}
	}
	if _, err := fmt.Fprint(term, ansi.SetSystemClipboard(text)); err != nil {
		return fmt.Errorf("no clipboard tool, and the terminal did not take it: %w", err)
	}
	return nil
}
