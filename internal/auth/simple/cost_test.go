package simple

import (
	"testing"

	"golang.org/x/crypto/bcrypt"
)

// Production hashing must stay at bcrypt.DefaultCost; only tests lower it.
func TestDefaultBcryptCost(t *testing.T) {
	if BcryptCost != bcrypt.DefaultCost {
		t.Fatalf("BcryptCost = %d, want bcrypt.DefaultCost (%d)", BcryptCost, bcrypt.DefaultCost)
	}
	h, err := HashPassword("pw")
	if err != nil {
		t.Fatal(err)
	}
	if c, err := bcrypt.Cost([]byte(h)); err != nil || c != bcrypt.DefaultCost {
		t.Fatalf("hash cost = %d, %v", c, err)
	}
}
