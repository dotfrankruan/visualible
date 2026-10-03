package store

import (
	"os"
	"testing"

	"github.com/dotfrankruan/visualible/internal/logging"
)

// TestMain keeps the store tests quiet: the package logs real application
// events, which are useful in production but noise in test output.
func TestMain(m *testing.M) {
	_ = logging.Setup(logging.Config{Level: "error"})
	os.Exit(m.Run())
}
