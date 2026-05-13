package narrative

import (
	"testing"

	"github.com/jcrussell/ralph/internal/bd"
	"github.com/jcrussell/ralph/internal/fsm"
)

func TestCompose(t *testing.T) {
	clean := fsm.Outcome{State: fsm.StateClean}
	dirty := fsm.Outcome{State: fsm.StateDirty}
	doneQE := fsm.Outcome{State: fsm.StateDone, Reason: fsm.ReasonQueueEmpty}
	failedAuth := fsm.Outcome{State: fsm.StateFailed, Reason: fsm.ReasonAuth}

	cases := []struct {
		name string
		in   Input
		want string
	}{
		{
			name: "prefix only when nothing happened",
			in:   Input{Prev: clean, Next: clean},
			want: "clean → clean",
		},
		{
			name: "the canonical example from the plan",
			in: Input{
				Prev:    clean,
				Next:    clean,
				Diff:    bd.Diff{InProgress: []string{"angr-z8xa"}, Closed: []string{"angr-z8xa"}},
				Commits: 2,
				Gate:    GatePassed,
			},
			want: "clean → clean: claimed angr-z8xa, 2 commits, closed angr-z8xa, gate green",
		},
		{
			name: "1 commit is singular",
			in:   Input{Prev: clean, Next: clean, Commits: 1},
			want: "clean → clean: 1 commit",
		},
		{
			name: "many commits, several closed",
			in: Input{
				Prev:    clean,
				Next:    clean,
				Diff:    bd.Diff{Closed: []string{"a", "b"}},
				Commits: 5,
			},
			want: "clean → clean: 5 commits, closed a, b",
		},
		{
			name: "three closed collapses to +N more",
			in: Input{
				Prev: clean,
				Next: clean,
				Diff: bd.Diff{Closed: []string{"a", "b", "c"}},
			},
			want: "clean → clean: closed a +2 more",
		},
		{
			name: "terminal includes Outcome.String reason",
			in:   Input{Prev: clean, Next: doneQE},
			want: "clean → done{queue_empty}",
		},
		{
			name: "failure adds trailing failure segment",
			in: Input{
				Prev:        clean,
				Next:        failedAuth,
				FailureMode: "auth",
			},
			want: "clean → failed{auth}: failure: auth",
		},
		{
			name: "failure mode ignored on non-failed terminals",
			in: Input{
				Prev:        clean,
				Next:        doneQE,
				FailureMode: "auth", // bogus on done; must be dropped
			},
			want: "clean → done{queue_empty}",
		},
		{
			name: "gate red",
			in:   Input{Prev: dirty, Next: clean, Gate: GateFailed},
			want: "dirty → clean: gate red",
		},
		{
			name: "gate skipped",
			in:   Input{Prev: clean, Next: clean, Gate: GateSkipped},
			want: "clean → clean: gate skipped",
		},
		{
			name: "gate not-run",
			in:   Input{Prev: clean, Next: clean, Gate: GateNotRun},
			want: "clean → clean: gate not run",
		},
		{
			name: "unknown gate falls through verbatim",
			in:   Input{Prev: clean, Next: clean, Gate: "weird"},
			want: "clean → clean: gate weird",
		},
		{
			name: "opened + created + deferred all render",
			in: Input{
				Prev: clean,
				Next: clean,
				Diff: bd.Diff{
					Opened:   []string{"a"},
					Created:  []string{"b", "c"},
					Deferred: []string{"d"},
				},
			},
			want: "clean → clean: opened a, created b, c, deferred d",
		},
		{
			name: "full house: every segment in canonical order",
			in: Input{
				Prev:    clean,
				Next:    clean,
				Commits: 3,
				Diff: bd.Diff{
					InProgress: []string{"wip"},
					Closed:     []string{"c1"},
					Opened:     []string{"o1"},
					Created:    []string{"n1"},
					Deferred:   []string{"d1"},
				},
				Gate: GatePassed,
			},
			want: "clean → clean: claimed wip, 3 commits, closed c1, opened o1, created n1, deferred d1, gate green",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := Compose(c.in); got != c.want {
				t.Errorf("got  %q\nwant %q", got, c.want)
			}
		})
	}
}

func TestComposeIsPure(t *testing.T) {
	in := Input{
		Prev:    fsm.Outcome{State: fsm.StateClean},
		Next:    fsm.Outcome{State: fsm.StateClean},
		Diff:    bd.Diff{Closed: []string{"a"}},
		Commits: 1,
		Gate:    GatePassed,
	}
	want := Compose(in)
	for range 5 {
		if got := Compose(in); got != want {
			t.Fatalf("non-deterministic: %q vs %q", want, got)
		}
	}
}
