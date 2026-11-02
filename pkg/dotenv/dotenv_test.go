package dotenv

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// LoadEnv reads .env from the process working directory, so each case chdir's
// into a temp dir to control whether a .env file is present.
func withWorkdir(t *testing.T, dir string) {
	t.Helper()
	orig, err := os.Getwd()
	require.NoError(t, err)
	require.NoError(t, os.Chdir(dir))
	t.Cleanup(func() { _ = os.Chdir(orig) })
}

func TestLoadEnv_FilePresent(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, ".env"), []byte("DOTENV_TEST_KEY=hello\n"), 0o600))
	withWorkdir(t, dir)
	t.Cleanup(func() { _ = os.Unsetenv("DOTENV_TEST_KEY") })

	LoadEnv()

	assert.Equal(t, "hello", os.Getenv("DOTENV_TEST_KEY"))
}

func TestLoadEnv_FileMissing(t *testing.T) {
	withWorkdir(t, t.TempDir()) // empty temp dir, no .env
	assert.NotPanics(t, func() { LoadEnv() })
}
