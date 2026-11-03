package agui

import (
	"encoding/json"
	"testing"
)

func TestTextOf(t *testing.T) {
	for raw, want := range map[string]string{
		`"hello"`: "hello",
		`[{"type":"text","text":"a"},{"type":"image","text":"x"},{"type":"text","text":"b"}]`: "ab",
		`42`: "",
	} {
		if got := textOf(json.RawMessage(raw)); got != want {
			t.Errorf("%s: %q, want %q", raw, got, want)
		}
	}
}

func TestPointerEscape(t *testing.T) {
	if got := pointerEscape("ui:a/b~c"); got != "ui:a~1b~0c" {
		t.Errorf("%q", got)
	}
}

func TestNewInputNeedsUserMessage(t *testing.T) {
	if _, err := newInput([]Message{{Role: "assistant", Content: json.RawMessage(`"x"`)}}, nil); err == nil {
		t.Error("no user message accepted")
	}
	c, err := newInput([]Message{{Role: "user", Content: json.RawMessage(`"first"`)}, {Role: "assistant", Content: json.RawMessage(`"y"`)}, {Role: "user", Content: json.RawMessage(`"last"`)}}, nil)
	if err != nil || c.Parts[0].Text != "last" {
		t.Errorf("only the last user message is input: %v %v", c, err)
	}
}
