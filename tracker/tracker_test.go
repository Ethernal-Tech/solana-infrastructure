package tracker

import (
	"testing"
	"time"

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

func TestChainHeadCatchUpWait(t *testing.T) {
	t.Parallel()

	t.Run("enough blocks needs no wait", func(t *testing.T) {
		t.Parallel()
		require.Equal(t, time.Duration(0), chainHeadCatchUpWait(13))
		require.Equal(t, time.Duration(0), chainHeadCatchUpWait(15))
	})

	t.Run("partial batch waits for remaining slots", func(t *testing.T) {
		t.Parallel()
		require.Equal(t, 2400*time.Millisecond, chainHeadCatchUpWait(7))
	})

	t.Run("empty result waits for full target", func(t *testing.T) {
		t.Parallel()
		require.Equal(t, 5200*time.Millisecond, chainHeadCatchUpWait(0))
	})
}
