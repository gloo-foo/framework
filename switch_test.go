package gloo

import "testing"

type testFlags struct {
	Verbose bool
	Count   int
}

type verboseSwitch struct{}

func (verboseSwitch) Configure(f *testFlags) { f.Verbose = true }

type countSwitch int

func (c countSwitch) Configure(f *testFlags) { f.Count = int(c) }

func TestConfigureNoSwitches(t *testing.T) {
	flags := configure[testFlags]()
	if flags.Verbose || flags.Count != 0 {
		t.Errorf("zero switches should yield zero value, got %+v", flags)
	}
}

func TestConfigureAppliesAllSwitches(t *testing.T) {
	flags := configure(verboseSwitch{}, countSwitch(3))
	if !flags.Verbose {
		t.Error("Verbose should be set")
	}
	if flags.Count != 3 {
		t.Errorf("Count = %d, want 3", flags.Count)
	}
}

func TestConfigureSkipsNilSwitch(t *testing.T) {
	flags := configure(nil, verboseSwitch{}, nil)
	if !flags.Verbose {
		t.Error("nil switches should be skipped, non-nil applied")
	}
}

func TestConfigureLastSwitchWins(t *testing.T) {
	flags := configure(countSwitch(1), countSwitch(9))
	if flags.Count != 9 {
		t.Errorf("Count = %d, want 9 (later switch overrides)", flags.Count)
	}
}
