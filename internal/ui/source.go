package ui

import (
	"github.com/charmbracelet/lipgloss"

	"github.com/PatrickSteil/ytplay/internal/yt"
)

// A song's source is shown as a one-cell coloured dot in front of its title.

// sourceIcon is the dot of a source: red for YouTube, orange for SoundCloud.
func sourceIcon(s yt.Source) string {
	colour := "196"
	if s == yt.SoundCloud {
		colour = "208"
	}
	return lipgloss.NewStyle().Foreground(lipgloss.Color(colour)).Render("●")
}

// iconOfEntry is the dot of a playlist entry, from its address.
func iconOfEntry(e playlistEntry) string { return sourceIcon(yt.SourceOf(yt.KeyOf(e.Filename))) }
