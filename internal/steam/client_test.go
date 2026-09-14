package steam

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

func TestGuardCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	a := &Adapter{ctx: ctx, events: make(chan Event, 1), guard: make(chan string, 1)}
	auth := &authenticator{a, ctx}
	done := make(chan error, 1)
	go func() { _, err := auth.GetDeviceCode(true); done <- err }()
	ev := <-a.events
	if ev.Kind != "guard" || !ev.Wrong {
		t.Fatal(ev)
	}
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("guard goroutine leaked")
	}
}
func TestNoRawErrorsOrNetworkOnConstruction(t *testing.T) {
	raw := errors.New("https://host/path?access_token=SECRET")
	if strings.Contains(Safe(raw).Error(), "SECRET") {
		t.Fatal("secret leak")
	}
	c, err := New("test")
	if err != nil {
		t.Fatal(err)
	}
	if c.State().Online {
		t.Fatal("unexpected online")
	}
	if err = c.Close(); err != nil {
		t.Fatal(err)
	}
	_ = c.Close()
}
