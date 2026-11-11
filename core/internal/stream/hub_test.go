package stream

import "testing"

func TestHub(t *testing.T) {
	h := NewHub[int](2)
	if _, ok := h.Last(); ok {
		t.Fatal("a new hub has no last value")
	}
	a, b := h.Subscribe(), h.Subscribe()
	h.Publish(1)
	if v := <-a.C; v != 1 {
		t.Errorf("a got %d", v)
	}
	if v := <-b.C; v != 1 {
		t.Errorf("b got %d", v)
	}
	if v, ok := h.Last(); !ok || v != 1 {
		t.Errorf("last = %d, %v", v, ok)
	}

	// b does not read: after its buffer of 2 is full, it loses values,
	// and a still gets all of them.
	for i := 2; i <= 5; i++ {
		h.Publish(i)
		if v := <-a.C; v != i {
			t.Errorf("a got %d, want %d", v, i)
		}
	}
	if b.Dropped() != 2 {
		t.Errorf("b dropped %d, want 2", b.Dropped())
	}
	if v := <-b.C; v != 2 {
		t.Errorf("b got %d, want 2", v)
	}

	a.Close()
	a.Close()
	if _, open := <-a.C; open {
		t.Error("C must be closed after Close")
	}
	if h.Subscribers() != 1 {
		t.Errorf("subscribers = %d", h.Subscribers())
	}
	h.Publish(6) // must not panic on the closed subscription
}
