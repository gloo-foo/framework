package verification

import (
	"bytes"
	"testing"

	gloo "github.com/gloo-foo/framework"
)

func TestOutputString(t *testing.T) {
	var buf bytes.Buffer
	if err := gloo.OutputString(&buf, "hello"); err != nil {
		t.Fatal(err)
	}
	if buf.String() != "hello\n" {
		t.Errorf("expected %q, got %q", "hello\n", buf.String())
	}
}

func TestOutputStringWithTerminator(t *testing.T) {
	var buf bytes.Buffer
	if err := gloo.OutputStringWithTerminator(&buf, '\t', "hello"); err != nil {
		t.Fatal(err)
	}
	if buf.String() != "hello\t" {
		t.Errorf("expected %q, got %q", "hello\t", buf.String())
	}
}

func TestOutputStringEmpty(t *testing.T) {
	var buf bytes.Buffer
	if err := gloo.OutputString(&buf, ""); err != nil {
		t.Fatal(err)
	}
	if buf.String() != "\n" {
		t.Errorf("expected %q, got %q", "\n", buf.String())
	}
}
