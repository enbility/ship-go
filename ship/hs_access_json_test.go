package ship

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestDetectAccessMethodsMessageType(t *testing.T) {
	tests := []struct {
		name          string
		input         []byte
		expectedType  string
		expectedError bool
	}{
		// AccessMethodsRequest tests
		{
			name:          "AccessMethodsRequest without spaces",
			input:         []byte(`{"accessMethodsRequest":{}}`),
			expectedType:  "request",
			expectedError: false,
		},
		{
			name:          "AccessMethodsRequest with spaces",
			input:         []byte(`{"accessMethodsRequest": {}}`),
			expectedType:  "request",
			expectedError: false,
		},
		{
			name:          "AccessMethodsRequest with multiple spaces",
			input:         []byte(`{"accessMethodsRequest"  :  {  }}`),
			expectedType:  "request",
			expectedError: false,
		},
		{
			name:          "AccessMethodsRequest with tabs",
			input:         []byte(`{"accessMethodsRequest":	{}}`),
			expectedType:  "request",
			expectedError: false,
		},
		{
			name:          "AccessMethodsRequest with newlines",
			input:         []byte(`{
				"accessMethodsRequest": {
				}
			}`),
			expectedType:  "request",
			expectedError: false,
		},

		// AccessMethods tests
		{
			name:          "AccessMethods without spaces",
			input:         []byte(`{"accessMethods":{"id":"test"}}`),
			expectedType:  "methods",
			expectedError: false,
		},
		{
			name:          "AccessMethods with spaces",
			input:         []byte(`{"accessMethods": {"id": "test"}}`),
			expectedType:  "methods",
			expectedError: false,
		},
		{
			name:          "AccessMethods with mixed whitespace",
			input:         []byte(`{	"accessMethods"  :
				{  "id"	: "test" }  }`),
			expectedType:  "methods",
			expectedError: false,
		},

		// Error cases
		{
			name:          "Empty object",
			input:         []byte(`{}`),
			expectedType:  "",
			expectedError: true,
		},
		{
			name:          "Invalid JSON",
			input:         []byte(`{invalid json`),
			expectedType:  "",
			expectedError: true,
		},
		{
			name:          "Wrong message type",
			input:         []byte(`{"someOtherMessage": {}}`),
			expectedType:  "",
			expectedError: true,
		},
		{
			name:          "Both message types present",
			input:         []byte(`{"accessMethodsRequest": {}, "accessMethods": {"id": "test"}}`),
			expectedType:  "request", // Should detect first one
			expectedError: false,
		},
		{
			name:          "Null value for accessMethods",
			input:         []byte(`{"accessMethods": null}`),
			expectedType:  "methods",
			expectedError: false,
		},
		{
			name:          "Array instead of object",
			input:         []byte(`[]`),
			expectedType:  "",
			expectedError: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			msgType, err := detectAccessMethodsMessageType(tt.input)
			
			if tt.expectedError {
				assert.Error(t, err, "Expected error but got none")
			} else {
				assert.NoError(t, err, "Expected no error but got: %v", err)
				assert.Equal(t, tt.expectedType, msgType, "Message type mismatch")
			}
		})
	}
}