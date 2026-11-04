package connector

import (
	"context"
	"fmt"
	"sync"
	"testing"
)

type stubConn struct{ id string }

func (s *stubConn) SourceID() string                              { return s.id }
func (s *stubConn) Sync(context.Context) error                    { return nil }
func (s *stubConn) RegisterWebhook(context.Context, string) error { return nil }
func (s *stubConn) ValidateWebhook(context.Context, []byte, map[string]string) (string, error) {
	return s.id, nil
}
func (s *stubConn) WriteEvent(context.Context, *RemoteEvent) (string, error) { return "", nil }
func (s *stubConn) DeleteEvent(context.Context, string) error                { return nil }

func TestRegistryBasics(t *testing.T) {
	r := NewRegistry()
	if _, ok := r.Get("a"); ok {
		t.Fatal("empty registry returned a connector")
	}
	a, b := &stubConn{"a"}, &stubConn{"b"}
	r.Replace(map[string]Connector{"b": b, "a": a})
	if got, ok := r.Get("a"); !ok || got != Connector(a) {
		t.Fatalf("Get(a) = %v, %v", got, ok)
	}
	ids := r.IDs()
	if len(ids) != 2 || ids[0] != "a" || ids[1] != "b" {
		t.Fatalf("IDs = %v, want sorted [a b]", ids)
	}
	snap := r.Snapshot()
	delete(snap, "a")
	if _, ok := r.Get("a"); !ok {
		t.Fatal("mutating Snapshot affected the registry")
	}
	in := map[string]Connector{"c": &stubConn{"c"}}
	r.Replace(in)
	delete(in, "c")
	if _, ok := r.Get("c"); !ok {
		t.Fatal("Replace kept a reference to the caller's map")
	}
}

func TestRegistryNilGet(t *testing.T) {
	var r *Registry
	if _, ok := r.Get("x"); ok {
		t.Fatal("nil registry returned a connector")
	}
}

func TestRegistryConcurrentGetReplace(t *testing.T) {
	r := NewRegistry()
	var wg sync.WaitGroup
	for i := 0; i < 4; i++ {
		wg.Add(2)
		go func() {
			defer wg.Done()
			for j := 0; j < 500; j++ {
				id := fmt.Sprintf("s%d", j%5)
				r.Replace(map[string]Connector{id: &stubConn{id}})
			}
		}()
		go func() {
			defer wg.Done()
			for j := 0; j < 500; j++ {
				r.Get("s1")
				r.IDs()
				r.Snapshot()
			}
		}()
	}
	wg.Wait()
}
