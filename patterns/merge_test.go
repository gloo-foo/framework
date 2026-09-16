package patterns_test

import (
	"context"
	"errors"
	"testing"

	gloo "github.com/gloo-foo/framework"
	"github.com/gloo-foo/framework/patterns"
)

func lessInt(a, b int) bool { return a < b }

func TestMergeSorted_InterleavesInOrder(t *testing.T) {
	a := gloo.StreamOf(1, 3, 5, 7)
	b := gloo.StreamOf(2, 3, 4)
	got, err := patterns.MergeSorted(context.Background(), lessInt, a, b).Collect()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := []int{1, 2, 3, 3, 4, 5, 7}
	if len(got) != len(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("got[%d] = %d, want %d", i, got[i], want[i])
		}
	}
}

func TestMergeSorted_OneSideEmpty(t *testing.T) {
	got, err := patterns.MergeSorted(context.Background(), lessInt, gloo.StreamOf[int](), gloo.StreamOf(1, 2)).Collect()
	if err != nil || len(got) != 2 {
		t.Fatalf("got %v err=%v, want the non-empty side", got, err)
	}
	got, err = patterns.MergeSorted(context.Background(), lessInt, gloo.StreamOf(1, 2), gloo.StreamOf[int]()).Collect()
	if err != nil || len(got) != 2 {
		t.Fatalf("got %v err=%v, want the non-empty side", got, err)
	}
}

func TestMergeSorted_BothEmpty(t *testing.T) {
	got, err := patterns.MergeSorted(context.Background(), lessInt, gloo.StreamOf[int](), gloo.StreamOf[int]()).
		Collect()
	if err != nil || len(got) != 0 {
		t.Fatalf("got %v err=%v, want empty", got, err)
	}
}

// errThenEndless yields one value, then an error — while the OTHER side keeps
// producing, so the merge must stop it.
func TestMergeSorted_ErrorStopsBothInputs(t *testing.T) {
	wantErr := errors.New("side a broke")
	aDone := make(chan struct{})
	a := gloo.Generate(context.Background(), func(_ context.Context, send func(int) bool, sendErr func(error)) {
		defer close(aDone)
		if send(1) {
			sendErr(wantErr)
		}
	})
	bDone := make(chan struct{})
	b := gloo.Generate(context.Background(), func(_ context.Context, send func(int) bool, _ func(error)) {
		defer close(bDone)
		i := 0
		for send(i) {
			i += 2
		}
	})
	_, err := patterns.MergeSorted(context.Background(), lessInt, a, b).Collect()
	if !errors.Is(err, wantErr) {
		t.Fatalf("got %v, want side a's error", err)
	}
	waitClosed(t, aDone, "merge error side")
	waitClosed(t, bDone, "merge surviving side")
}

// A late error — on the side still draining after the other side ended — must
// surface, not vanish.
func TestMergeSorted_LateErrorOnRemainingSideSurfaces(t *testing.T) {
	wantErr := errors.New("late failure")
	a := gloo.StreamOf(1)
	b := gloo.Generate(context.Background(), func(_ context.Context, send func(int) bool, sendErr func(error)) {
		if send(2) && send(3) {
			sendErr(wantErr)
		}
	})
	_, err := patterns.MergeSorted(context.Background(), lessInt, a, b).Collect()
	if !errors.Is(err, wantErr) {
		t.Errorf("got %v, want the late error", err)
	}
}

func TestMergeSorted_DownstreamStopStopsBothInputs(t *testing.T) {
	aDone := make(chan struct{})
	a := gloo.Generate(context.Background(), func(_ context.Context, send func(int) bool, _ func(error)) {
		defer close(aDone)
		i := 0
		for send(i) {
			i += 2
		}
	})
	bDone := make(chan struct{})
	b := gloo.Generate(context.Background(), func(_ context.Context, send func(int) bool, _ func(error)) {
		defer close(bDone)
		i := 1
		for send(i) {
			i += 2
		}
	})
	merged := patterns.MergeSorted(context.Background(), lessInt, a, b)
	got, err := patterns.Take[int](5).Execute(context.Background(), merged).Collect()
	if err != nil || len(got) != 5 {
		t.Fatalf("got %v err=%v, want five items", got, err)
	}
	waitClosed(t, aDone, "merge input a after downstream stop")
	waitClosed(t, bDone, "merge input b after downstream stop")
}

// A late error on the FIRST input — still draining after the second ended —
// must surface too (the mirror of the b-side case).
func TestMergeSorted_LateErrorOnFirstSideSurfaces(t *testing.T) {
	wantErr := errors.New("late failure on a")
	a := gloo.Generate(context.Background(), func(_ context.Context, send func(int) bool, sendErr func(error)) {
		if send(2) && send(3) {
			sendErr(wantErr)
		}
	})
	b := gloo.StreamOf(1)
	_, err := patterns.MergeSorted(context.Background(), lessInt, a, b).Collect()
	if !errors.Is(err, wantErr) {
		t.Errorf("got %v, want the late error", err)
	}
}

// A downstream stop while the surviving side is draining its remainder must
// end the merge and stop that side.
func TestMergeSorted_DownstreamStopDuringRemainderDrain(t *testing.T) {
	aDone := make(chan struct{})
	a := gloo.Generate(context.Background(), func(_ context.Context, send func(int) bool, _ func(error)) {
		defer close(aDone)
		i := 1
		for send(i) {
			i++
		}
	})
	b := gloo.StreamOf(0)
	merged := patterns.MergeSorted(context.Background(), lessInt, a, b)
	got, err := patterns.Take[int](3).Execute(context.Background(), merged).Collect()
	if err != nil || len(got) != 3 {
		t.Fatalf("got %v err=%v, want three items", got, err)
	}
	waitClosed(t, aDone, "surviving side after downstream stop")
}
