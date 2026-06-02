package tracker

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestGetSlotsToQueryBlocks(t *testing.T) {
	t.Parallel()

	testCases := []struct {
		name      string
		input     []uint64
		threshold uint64
		expected  []uint64
	}{
		{
			name:      "includes exact threshold slots",
			input:     []uint64{100, 101, 102, 200, 201, 300},
			threshold: 100,
			expected:  []uint64{100, 200, 300},
		},
		{
			name:      "includes last block before next threshold when boundary slot missing",
			input:     []uint64{95, 99, 101, 150, 199, 205},
			threshold: 100,
			expected:  []uint64{99, 199},
		},
		{
			name:      "does not duplicate when next slot is exact threshold",
			input:     []uint64{98, 99, 100, 101},
			threshold: 100,
			expected:  []uint64{100},
		},
		{
			name:      "empty input",
			input:     []uint64{},
			threshold: 100,
			expected:  []uint64{},
		},
	}

	for _, tc := range testCases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			result := getSlotsToQueryBlocks(tc.input, tc.threshold)
			require.Equal(t, tc.expected, result)
		})
	}
}
