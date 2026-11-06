package ui

import "testing"

func TestSeekPercentForKey(t *testing.T) {
	tests := []struct {
		key  string
		want int
		ok   bool
	}{
		{"0", 0, true},
		{"5", 50, true},
		{"9", 90, true},
		{"=", 100, true},
		{"a", 0, false},
		{"10", 0, false},
		{"", 0, false},
	}
	for _, tt := range tests {
		got, ok := seekPercentForKey(tt.key)
		if got != tt.want || ok != tt.ok {
			t.Errorf("seekPercentForKey(%q) = %d, %v; want %d, %v", tt.key, got, ok, tt.want, tt.ok)
		}
	}
}
