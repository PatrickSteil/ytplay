package player

import (
	"reflect"
	"testing"
)

func q(pos int, urls ...string) *queue { return &queue{urls: urls, pos: pos} }

func wantQueue(t *testing.T, got *queue, pos int, urls ...string) {
	t.Helper()
	if got.pos != pos || !reflect.DeepEqual(got.urls, urls) {
		t.Errorf("queue = %v at %d, want %v at %d", got.urls, got.pos, urls, pos)
	}
}

func TestAppend(t *testing.T) {
	x := q(1, "a", "b")
	x.append("c", "d")
	wantQueue(t, x, 1, "a", "b", "c", "d")
}

func TestInsertAfterCurrent(t *testing.T) {
	x := q(1, "a", "b", "c", "d")
	if i := x.insertAfterCurrent("n"); i != 2 {
		t.Errorf("index = %d, want 2", i)
	}
	wantQueue(t, x, 1, "a", "b", "n", "c", "d")

	idle := q(-1, "a", "b")
	if i := idle.insertAfterCurrent("n"); i != 2 {
		t.Errorf("idle: index = %d, want 2 (appended)", i)
	}
	wantQueue(t, idle, -1, "a", "b", "n")
}

func TestRemoveBeforeCurrentShiftsPos(t *testing.T) {
	x := q(2, "a", "b", "c", "d")
	if err := x.remove(0); err != nil {
		t.Fatal(err)
	}
	wantQueue(t, x, 1, "b", "c", "d")
}

func TestRemoveAfterCurrentKeepsPos(t *testing.T) {
	x := q(1, "a", "b", "c")
	if err := x.remove(2); err != nil {
		t.Fatal(err)
	}
	wantQueue(t, x, 1, "a", "b")
}

func TestRemoveCurrentLeavesPosOnTheFollowingEntry(t *testing.T) {
	x := q(1, "a", "b", "c")
	if err := x.remove(1); err != nil {
		t.Fatal(err)
	}
	wantQueue(t, x, 1, "a", "c")
}

func TestRemoveCurrentWhenLastGoesIdle(t *testing.T) {
	x := q(1, "a", "b")
	if err := x.remove(1); err != nil {
		t.Fatal(err)
	}
	wantQueue(t, x, -1, "a")
}

func TestRemoveOutOfRange(t *testing.T) {
	x := q(0, "a", "b")
	for _, i := range []int{-1, 2, 99} {
		if err := x.remove(i); err == nil {
			t.Errorf("remove(%d) should fail", i)
		}
	}
	wantQueue(t, x, 0, "a", "b")
}

func TestSwapFollowsTheCurrentEntry(t *testing.T) {
	x := q(1, "a", "b", "c")
	if err := x.swap(1, 2); err != nil {
		t.Fatal(err)
	}
	wantQueue(t, x, 2, "a", "c", "b")
	if err := x.swap(1, 2); err != nil { // the other way round: current is the second index
		t.Fatal(err)
	}
	wantQueue(t, x, 1, "a", "b", "c")
}

func TestSwapLeavesPosAloneWhenNotInvolved(t *testing.T) {
	x := q(0, "a", "b", "c")
	if err := x.swap(1, 2); err != nil {
		t.Fatal(err)
	}
	wantQueue(t, x, 0, "a", "c", "b")
}

func TestSwapRejectsNonNeighboursAndRange(t *testing.T) {
	x := q(0, "a", "b", "c")
	for _, ij := range [][2]int{{0, 2}, {1, 1}, {-1, 0}, {2, 3}, {3, 2}} {
		if err := x.swap(ij[0], ij[1]); err == nil {
			t.Errorf("swap(%d, %d) should fail", ij[0], ij[1])
		}
	}
	wantQueue(t, x, 0, "a", "b", "c")
}

func TestClearExceptCurrent(t *testing.T) {
	x := q(2, "a", "b", "c", "d", "e")
	x.clearExceptCurrent()
	wantQueue(t, x, 0, "c")

	idle := q(-1, "a", "b")
	idle.clearExceptCurrent()
	wantQueue(t, idle, -1)
}

func TestUpcoming(t *testing.T) {
	x := q(1, "a", "b", "c", "d")
	got := x.upcoming()
	if !reflect.DeepEqual(got, []string{"c", "d"}) {
		t.Errorf("upcoming = %v", got)
	}
	got[0] = "changed" // a copy: must not write through
	wantQueue(t, x, 1, "a", "b", "c", "d")
	if u := q(-1, "a", "b").upcoming(); len(u) != 0 {
		t.Errorf("idle upcoming = %v, want none", u)
	}
	if u := q(1, "a", "b").upcoming(); len(u) != 0 {
		t.Errorf("last upcoming = %v, want none", u)
	}
}

func TestQueueReorderUpcoming(t *testing.T) {
	x := q(1, "a", "b", "c", "d")
	if err := x.reorderUpcoming([]string{"d", "c"}); err != nil {
		t.Fatal(err)
	}
	wantQueue(t, x, 1, "a", "b", "d", "c")
}

func TestQueueReorderUpcomingRejectsADifferentSet(t *testing.T) {
	for name, urls := range map[string][]string{
		"missing":   {"c"},
		"extra":     {"c", "d", "e"},
		"different": {"c", "x"},
		"played":    {"c", "a"},
	} {
		x := q(1, "a", "b", "c", "d")
		if err := x.reorderUpcoming(urls); err == nil {
			t.Errorf("%s: expected an error", name)
		}
		wantQueue(t, x, 1, "a", "b", "c", "d")
	}
	idle := q(-1, "a", "b")
	if err := idle.reorderUpcoming([]string{"a"}); err == nil {
		t.Error("an idle queue has no upcoming songs to reorder")
	}
}

func TestQueueReorderUpcomingWithDuplicateURLs(t *testing.T) {
	x := q(0, "p", "x", "y", "x")
	if err := x.reorderUpcoming([]string{"x", "x", "y"}); err != nil {
		t.Fatal(err)
	}
	wantQueue(t, x, 0, "p", "x", "x", "y")
	if err := x.reorderUpcoming([]string{"x", "y", "y"}); err == nil {
		t.Error("a wrong count of a duplicate must be rejected")
	}
	wantQueue(t, x, 0, "p", "x", "x", "y")
}
