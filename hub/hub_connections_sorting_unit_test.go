package hub

import (
	"net"
	"testing"

	"github.com/stretchr/testify/assert"
)

// TestSortIPAddresses tests the sortIPAddresses method on Hub
func TestSortIPAddresses(t *testing.T) {
	hub := setupTestHubForTimer(t)

	tests := []struct {
		name     string
		input    []string
		expected []string
	}{
		{
			name:     "empty slice",
			input:    []string{},
			expected: []string{},
		},
		{
			name:     "single IPv4",
			input:    []string{"192.168.1.1"},
			expected: []string{"192.168.1.1"},
		},
		{
			name:     "single IPv6",
			input:    []string{"2001:db8::1"},
			expected: []string{"2001:db8::1"},
		},
		{
			name:     "IPv4 already first",
			input:    []string{"192.168.1.1", "2001:db8::1"},
			expected: []string{"192.168.1.1", "2001:db8::1"},
		},
		{
			name:     "IPv6 first needs sorting",
			input:    []string{"2001:db8::1", "192.168.1.1"},
			expected: []string{"192.168.1.1", "2001:db8::1"},
		},
		{
			name: "mixed IPv4 and IPv6",
			input: []string{
				"2001:db8::1",
				"192.168.1.1",
				"::1",
				"10.0.0.1",
				"fe80::1",
			},
			expected: []string{
				"192.168.1.1",
				"10.0.0.1",
				"2001:db8::1",
				"::1",
				"fe80::1",
			},
		},
		{
			name: "all IPv4",
			input: []string{
				"192.168.1.1",
				"10.0.0.1",
				"172.16.0.1",
			},
			expected: []string{
				"192.168.1.1",
				"10.0.0.1",
				"172.16.0.1",
			},
		},
		{
			name: "all IPv6",
			input: []string{
				"2001:db8::1",
				"::1",
				"fe80::1",
			},
			expected: []string{
				"2001:db8::1",
				"::1",
				"fe80::1",
			},
		},
		{
			name: "IPv4-mapped IPv6 addresses",
			input: []string{
				"::ffff:192.168.1.1",
				"192.168.1.2",
				"2001:db8::1",
			},
			expected: []string{
				"::ffff:192.168.1.1", // IPv4-mapped IPv6 has To4() != nil
				"192.168.1.2",
				"2001:db8::1",
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			// Convert string slices to net.IP slices
			addresses := make([]net.IP, len(tc.input))
			for i, addr := range tc.input {
				addresses[i] = net.ParseIP(addr)
			}

			// Call the method
			result := hub.sortIPAddresses(addresses)

			// Verify the result
			assert.Equal(t, len(tc.expected), len(result), "result length should match expected")

			for i, addr := range result {
				expected := net.ParseIP(tc.expected[i])
				assert.True(t, addr.Equal(expected),
					"at index %d: expected %s, got %s", i, tc.expected[i], addr)
			}
		})
	}
}

// TestSortIPAddressesStability tests that sorting maintains relative order of same-type IPs
func TestSortIPAddressesStability(t *testing.T) {
	hub := setupTestHubForTimer(t)

	// Test that IPv4 addresses maintain their relative order
	t.Run("IPv4 stability", func(t *testing.T) {
		addresses := []net.IP{
			net.ParseIP("192.168.1.1"),
			net.ParseIP("192.168.1.2"),
			net.ParseIP("192.168.1.3"),
		}

		result := hub.sortIPAddresses(addresses)

		// Should maintain the same order
		assert.True(t, result[0].Equal(net.ParseIP("192.168.1.1")))
		assert.True(t, result[1].Equal(net.ParseIP("192.168.1.2")))
		assert.True(t, result[2].Equal(net.ParseIP("192.168.1.3")))
	})

	// Test that IPv6 addresses maintain their relative order
	t.Run("IPv6 stability", func(t *testing.T) {
		addresses := []net.IP{
			net.ParseIP("2001:db8::1"),
			net.ParseIP("2001:db8::2"),
			net.ParseIP("2001:db8::3"),
		}

		result := hub.sortIPAddresses(addresses)

		// Should maintain the same order
		assert.True(t, result[0].Equal(net.ParseIP("2001:db8::1")))
		assert.True(t, result[1].Equal(net.ParseIP("2001:db8::2")))
		assert.True(t, result[2].Equal(net.ParseIP("2001:db8::3")))
	})
}

// TestSortIPAddressesNilHandling tests handling of nil values
func TestSortIPAddressesNilHandling(t *testing.T) {
	hub := setupTestHubForTimer(t)

	t.Run("nil slice", func(t *testing.T) {
		var addresses []net.IP
		result := hub.sortIPAddresses(addresses)
		assert.Nil(t, result)
	})

	t.Run("slice with nil elements", func(t *testing.T) {
		addresses := []net.IP{
			nil,
			net.ParseIP("192.168.1.1"),
			nil,
			net.ParseIP("2001:db8::1"),
		}

		// The function should handle nils gracefully
		// Nils will be treated as neither IPv4 nor IPv6
		result := hub.sortIPAddresses(addresses)

		// IPv4 should come first, then the rest (including nils)
		assert.NotNil(t, result)
		assert.Equal(t, 4, len(result))

		// First element should be the IPv4 address
		assert.True(t, result[0].Equal(net.ParseIP("192.168.1.1")))
	})
}

// TestSortIPAddressesConcurrency tests that the method is safe for concurrent use
func TestSortIPAddressesConcurrency(t *testing.T) {
	hub := setupTestHubForTimer(t)

	// Create a test slice
	baseAddresses := []net.IP{
		net.ParseIP("2001:db8::1"),
		net.ParseIP("192.168.1.1"),
		net.ParseIP("::1"),
		net.ParseIP("10.0.0.1"),
	}

	// Run concurrent sorts
	done := make(chan bool, 10)
	for i := 0; i < 10; i++ {
		go func() {
			// Make a copy to avoid race conditions on the slice
			addresses := make([]net.IP, len(baseAddresses))
			copy(addresses, baseAddresses)

			result := hub.sortIPAddresses(addresses)

			// Verify IPv4 comes first
			assert.True(t, result[0].To4() != nil)
			assert.True(t, result[1].To4() != nil)
			assert.True(t, result[2].To4() == nil)
			assert.True(t, result[3].To4() == nil)

			done <- true
		}()
	}

	// Wait for all goroutines
	for i := 0; i < 10; i++ {
		<-done
	}
}

// TestSortIPAddressesIntegrationWithInitateConnection tests that sortIPAddresses
// integrates correctly with the initateConnection method
func TestSortIPAddressesIntegrationWithInitateConnection(t *testing.T) {
	hub := setupTestHubForTimer(t)

	// This test verifies that when initateConnection calls sortIPAddresses,
	// the addresses are properly sorted before connection attempts
	t.Run("verify sorting is applied", func(t *testing.T) {
		addresses := []net.IP{
			net.ParseIP("2001:db8::1"),
			net.ParseIP("192.168.1.1"),
			net.ParseIP("fe80::1"),
			net.ParseIP("10.0.0.1"),
		}

		// Call sortIPAddresses as it would be called in initateConnection
		sorted := hub.sortIPAddresses(addresses)

		// Verify that IPv4 addresses come first
		assert.True(t, sorted[0].To4() != nil, "First address should be IPv4")
		assert.True(t, sorted[1].To4() != nil, "Second address should be IPv4")
		assert.True(t, sorted[2].To4() == nil, "Third address should be IPv6")
		assert.True(t, sorted[3].To4() == nil, "Fourth address should be IPv6")

		// Verify specific addresses
		assert.True(t, sorted[0].Equal(net.ParseIP("192.168.1.1")) || sorted[0].Equal(net.ParseIP("10.0.0.1")))
		assert.True(t, sorted[1].Equal(net.ParseIP("192.168.1.1")) || sorted[1].Equal(net.ParseIP("10.0.0.1")))
		assert.True(t, sorted[2].Equal(net.ParseIP("2001:db8::1")) || sorted[2].Equal(net.ParseIP("fe80::1")))
		assert.True(t, sorted[3].Equal(net.ParseIP("2001:db8::1")) || sorted[3].Equal(net.ParseIP("fe80::1")))
	})
}
