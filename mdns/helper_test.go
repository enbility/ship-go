package mdns

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestParseTXT(t *testing.T) {
	var txt []string

	result := parseTxt(txt)
	assert.Equal(t, 0, len(result))

	txt = []string{"test"}
	result = parseTxt(txt)
	assert.Equal(t, 0, len(result))

	txt = []string{"test=more"}
	result = parseTxt(txt)
	assert.Equal(t, 1, len(result))
}
