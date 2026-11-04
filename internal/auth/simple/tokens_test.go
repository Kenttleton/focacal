package simple

import (
	"sync"
	"testing"
)

func TestTokenRegistryConcurrentUse(t *testing.T) {
	t.Cleanup(RevokeAll)
	var wg sync.WaitGroup
	for range 32 {
		wg.Add(2)
		go func() {
			defer wg.Done()
			tok := GenerateKey()
			StoreToken(tok)
			if !IsValidToken(tok) {
				t.Errorf("stored token not valid")
			}
		}()
		go func() {
			defer wg.Done()
			_ = IsValidToken("nope")
		}()
	}
	wg.Add(1)
	go func() { defer wg.Done(); RevokeAll() }()
	wg.Wait()
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
