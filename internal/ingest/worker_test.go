package ingest

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestSubtitleFile(t *testing.T) {
	assert.Equal(t, "sub-en.vtt", SubtitleFile("en"))
	assert.Equal(t, "sub-pt-BR.vtt", SubtitleFile("pt-BR"))
	assert.Equal(t, "sub-___x.vtt", SubtitleFile("../x"))
}
