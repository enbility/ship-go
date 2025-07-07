package util

import (
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestIsRunningOnCI(t *testing.T) {
	tests := []struct {
		name     string
		envValue string
		want     bool
	}{
		{
			name:     "CI environment",
			envValue: "CI",
			want:     true,
		},
		{
			name:     "empty value",
			envValue: "",
			want:     false,
		},
		{
			name:     "different value",
			envValue: "LOCAL",
			want:     false,
		},
		{
			name:     "lowercase ci",
			envValue: "ci",
			want:     false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Save original value
			original := os.Getenv("ACTION_ENVIRONMENT")
			defer os.Setenv("ACTION_ENVIRONMENT", original)

			// Set test value
			os.Setenv("ACTION_ENVIRONMENT", tt.envValue)

			got := IsRunningOnCI()
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestPtr(t *testing.T) {
	t.Run("integer pointer", func(t *testing.T) {
		val := 42
		ptr := Ptr(val)
		assert.NotNil(t, ptr)
		assert.Equal(t, val, *ptr)
		// Modifying the original value shouldn't affect the pointer
		val = 100
		assert.Equal(t, 42, *ptr)
	})

	t.Run("string pointer", func(t *testing.T) {
		val := "test"
		ptr := Ptr(val)
		assert.NotNil(t, ptr)
		assert.Equal(t, val, *ptr)
	})

	t.Run("struct pointer", func(t *testing.T) {
		type TestStruct struct {
			Name string
			Age  int
		}
		val := TestStruct{Name: "John", Age: 30}
		ptr := Ptr(val)
		assert.NotNil(t, ptr)
		assert.Equal(t, val, *ptr)
	})

	t.Run("nil interface", func(t *testing.T) {
		var val interface{}
		ptr := Ptr(val)
		assert.NotNil(t, ptr)
		assert.Nil(t, *ptr)
	})
}

func TestDeepCopy(t *testing.T) {
	t.Run("simple struct", func(t *testing.T) {
		type Person struct {
			Name string `json:"name"`
			Age  int    `json:"age"`
		}

		source := &Person{Name: "Alice", Age: 25}
		dest := &Person{}

		DeepCopy(source, dest)

		assert.Equal(t, source.Name, dest.Name)
		assert.Equal(t, source.Age, dest.Age)
		assert.NotSame(t, source, dest) // Different objects
	})

	t.Run("nested struct", func(t *testing.T) {
		type Address struct {
			Street string `json:"street"`
			City   string `json:"city"`
		}
		type PersonWithAddress struct {
			Name    string  `json:"name"`
			Address Address `json:"address"`
		}

		source := &PersonWithAddress{
			Name: "Bob",
			Address: Address{
				Street: "123 Main St",
				City:   "Anytown",
			},
		}
		dest := &PersonWithAddress{}

		DeepCopy(source, dest)

		assert.Equal(t, source.Name, dest.Name)
		assert.Equal(t, source.Address.Street, dest.Address.Street)
		assert.Equal(t, source.Address.City, dest.Address.City)
	})

	t.Run("slice and map", func(t *testing.T) {
		type Container struct {
			Items []string          `json:"items"`
			Meta  map[string]string `json:"meta"`
		}

		source := &Container{
			Items: []string{"a", "b", "c"},
			Meta:  map[string]string{"key": "value"},
		}
		dest := &Container{}

		DeepCopy(source, dest)

		assert.Equal(t, source.Items, dest.Items)
		assert.Equal(t, source.Meta, dest.Meta)
	})

	t.Run("unexported fields are not copied", func(t *testing.T) {
		type PrivateData struct {
			Public  string `json:"public"`
			private string
		}

		source := &PrivateData{
			Public:  "visible",
			private: "hidden",
		}
		dest := &PrivateData{}

		DeepCopy(source, dest)

		assert.Equal(t, source.Public, dest.Public)
		assert.Empty(t, dest.private) // Private field not copied
	})

	t.Run("json tags control serialization", func(t *testing.T) {
		type Tagged struct {
			Included string `json:"included"`
			Excluded string `json:"-"`
			Renamed  string `json:"different_name"`
		}

		source := &Tagged{
			Included: "yes",
			Excluded: "no",
			Renamed:  "renamed",
		}
		dest := &Tagged{}

		DeepCopy(source, dest)

		assert.Equal(t, source.Included, dest.Included)
		assert.Empty(t, dest.Excluded) // Excluded by json tag
		assert.Equal(t, source.Renamed, dest.Renamed)
	})

	t.Run("nil source", func(t *testing.T) {
		type Simple struct {
			Value string `json:"value"`
		}

		var source *Simple
		dest := &Simple{Value: "initial"}

		DeepCopy(source, dest)

		// Dest should be unchanged when source is nil
		assert.Equal(t, "initial", dest.Value)
	})
}

func TestNormalizeSKI(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{
			name:     "already normalized",
			input:    "abc123def456",
			expected: "abc123def456",
		},
		{
			name:     "with spaces",
			input:    "abc 123 def 456",
			expected: "abc123def456",
		},
		{
			name:     "with dashes",
			input:    "abc-123-def-456",
			expected: "abc123def456",
		},
		{
			name:     "with uppercase",
			input:    "ABC123DEF456",
			expected: "abc123def456",
		},
		{
			name:     "mixed formatting",
			input:    "ABC-123 def-456",
			expected: "abc123def456",
		},
		{
			name:     "typical SKI format",
			input:    "12:34:56:78:9A:BC:DE:F0",
			expected: "12:34:56:78:9a:bc:de:f0",
		},
		{
			name:     "empty string",
			input:    "",
			expected: "",
		},
		{
			name:     "only spaces and dashes",
			input:    "  - - ",
			expected: "",
		},
		{
			name:     "multiple consecutive spaces and dashes",
			input:    "abc   ---   123",
			expected: "abc123",
		},
		{
			name:     "tabs and other whitespace",
			input:    "abc\t123\ndef",
			expected: "abc\t123\ndef", // Only spaces are removed
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := NormalizeSKI(tt.input)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestNormalizeSKI_Idempotent(t *testing.T) {
	// Test that normalizing an already normalized SKI doesn't change it
	testCases := []string{
		"abc123",
		"12:34:56:78:9a:bc:de:f0",
		"",
		"test",
	}

	for _, tc := range testCases {
		normalized := NormalizeSKI(tc)
		doubleNormalized := NormalizeSKI(normalized)
		assert.Equal(t, normalized, doubleNormalized, "NormalizeSKI should be idempotent for %s", tc)
	}
}