package plugin

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func Test_newInstanceName(t *testing.T) {
	name := newInstanceName("ci-vm-small-creating")

	require.True(t, hasInstanceNamePrefix(name, "ci-vm-small-creating"))
	require.False(t, hasInstanceNamePrefix(name, "ci-vm-small-running"))

	// Two names generated from the same base must be unique.
	other := newInstanceName("ci-vm-small-creating")
	require.NotEqual(t, name, other)
}

func Test_renameToState(t *testing.T) {
	tests := []struct {
		name        string
		currentName string
		newBase     string
		expected    string
	}{
		{
			name:        "preserves uuid suffix across rename",
			currentName: "ci-vm-small-creating-a1b2c3d4",
			newBase:     "ci-vm-small-running",
			expected:    "ci-vm-small-running-a1b2c3d4",
		},
		{
			name:        "no separator in current name",
			currentName: "nodash",
			newBase:     "ci-vm-small-removing",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := renameToState(tt.currentName, tt.newBase)

			if tt.expected != "" {
				require.Equal(t, tt.expected, got)
				return
			}

			require.True(t, hasInstanceNamePrefix(got, tt.newBase))
		})
	}
}

func Test_hasInstanceNamePrefix(t *testing.T) {
	require.True(t, hasInstanceNamePrefix("ci-vm-small-running-a1b2c3d4", "ci-vm-small-running"))
	require.False(t, hasInstanceNamePrefix("ci-vm-small-running-a1b2c3d4", "ci-vm-small-run"))
	require.False(t, hasInstanceNamePrefix("ci-vm-large-running-a1b2c3d4", "ci-vm-small-running"))
}
