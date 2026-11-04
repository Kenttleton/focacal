package simple

import (
	"sync"
	"testing"
)

func TestTokenRegistryConcurrentUse(t *testing.T) {
	t.Cleanup(RevokeAll)
	RevokeAll()
	// Phase 1: concurrent stores, lookups and revokes (race detector only;
	// a revoke may land between any store and lookup).
	var wg sync.WaitGroup
	for range 32 {
		wg.Add(3)
		go func() { defer wg.Done(); StoreToken(GenerateKey()) }()
		go func() { defer wg.Done(); _ = IsValidToken("nope") }()
		go func() { defer wg.Done(); RevokeAll() }()
	}
	wg.Wait()
	// Phase 2: concurrent stores with no revoke; every token must be valid.
	toks := make([]string, 32)
	for i := range toks {
		wg.Add(1)
		go func() { defer wg.Done(); toks[i] = GenerateKey(); StoreToken(toks[i]) }()
	}
	wg.Wait()
	for _, tok := range toks {
		if !IsValidToken(tok) {
			t.Fatalf("stored token %s not valid", tok)
		}
	}
}

func TestRevokeAllInvalidatesTokens(t *testing.T) {
	a, b := GenerateKey(), GenerateKey()
	StoreToken(a)
	StoreToken(b)
	if !IsValidToken(a) || !IsValidToken(b) {
		t.Fatal("tokens should be valid before RevokeAll")
	}
	RevokeAll()
	if IsValidToken(a) || IsValidToken(b) {
		t.Fatal("tokens still valid after RevokeAll")
	}
	c := GenerateKey()
	StoreToken(c)
	if !IsValidToken(c) {
		t.Fatal("token stored after RevokeAll should be valid")
	}
	RevokeAll()
}
