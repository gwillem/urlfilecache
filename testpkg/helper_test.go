package testpkg

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestUsePackageName(t *testing.T) {
	// Create a test server
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("test content"))
	}))
	defer ts.Close()

	// Get path with UsePackageName (should use "testpkg" as the package name)
	packagePath, err := FetchWithPackageName(ts.URL)
	require.NoError(t, err)
	defer os.Remove(packagePath)

	// Get path without UsePackageName (should use executable name)
	defaultPath, err := FetchDefault(ts.URL)
	require.NoError(t, err)
	defer os.Remove(defaultPath)

	execName := filepath.Base(os.Args[0])
	require.Contains(t, strings.Split(packagePath, "/"), "testpkg")
	require.NotContains(t, packagePath, execName, "path should not contain executable name")
	require.Contains(t, strings.Split(defaultPath, "/"), execName, "default path should contain executable name")

	// Paths should be different (unless executable is coincidentally named "testpkg")
	if execName != "testpkg" {
		require.NotEqual(t, defaultPath, packagePath, "paths should differ when using UsePackageName")
	}

	// Both files should exist and have the same content
	defaultContent, err := os.ReadFile(defaultPath)
	require.NoError(t, err)
	require.Equal(t, "test content", string(defaultContent))

	packageContent, err := os.ReadFile(packagePath)
	require.NoError(t, err)
	require.Equal(t, "test content", string(packageContent))
}
