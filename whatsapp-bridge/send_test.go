package main

import (
	"testing"

	"go.mau.fi/whatsmeow/types"
)

func TestParseRecipient(t *testing.T) {
	valid := map[string]types.JID{
		"447700900123":             types.NewJID("447700900123", types.DefaultUserServer),
		"+44 7700-900123":          types.NewJID("447700900123", types.DefaultUserServer),
		"123456789@s.whatsapp.net": types.NewJID("123456789", types.DefaultUserServer),
		"120363000000000000@g.us":  types.NewJID("120363000000000000", types.GroupServer),
	}
	for in, want := range valid {
		got, err := parseRecipient(in)
		if err != nil {
			t.Errorf("parseRecipient(%q) unexpected error: %v", in, err)
			continue
		}
		if got != want {
			t.Errorf("parseRecipient(%q) = %v, want %v", in, got, want)
		}
	}

	for _, in := range []string{"", "   ", "abc", "12", "1234567890; DROP"} {
		if _, err := parseRecipient(in); err == nil {
			t.Errorf("parseRecipient(%q) expected error", in)
		}
	}
}
