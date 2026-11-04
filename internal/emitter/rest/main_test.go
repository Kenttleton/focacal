package rest

import (
	"os"
	"testing"

	"golang.org/x/crypto/bcrypt"

	"github.com/Kenttleton/focacal/internal/auth/simple"
)

// TestMain lowers the bcrypt cost for this package's tests only (login
// tests hash many passwords). Production keeps bcrypt.DefaultCost, asserted
// in internal/auth/simple.
func TestMain(m *testing.M) {
	simple.BcryptCost = bcrypt.MinCost
	os.Exit(m.Run())
}
