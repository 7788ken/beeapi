package common

import (
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func withDebugEnabled(t *testing.T, enabled bool) {
	t.Helper()
	old := DebugEnabled
	DebugEnabled = enabled
	t.Cleanup(func() {
		DebugEnabled = old
	})
}

func TestLocalLogPreviewTruncatesLongContent(t *testing.T) {
	withDebugEnabled(t, false)
	content := strings.Repeat("a", LocalLogContentLimit+10)

	preview := LocalLogPreview(content)

	require.Len(t, preview[:LocalLogContentLimit], LocalLogContentLimit)
	require.Contains(t, preview, "truncated")
	require.Contains(t, preview, "original_length="+strconv.Itoa(LocalLogContentLimit+10))
	require.Contains(t, preview, "limit="+strconv.Itoa(LocalLogContentLimit))
}

func TestLocalLogPreviewKeepsLongContentWhenDebugEnabled(t *testing.T) {
	withDebugEnabled(t, true)
	content := strings.Repeat("a", LocalLogContentLimit+10)

	preview := LocalLogPreview(content)

	require.Equal(t, content, preview)
}

func TestLocalLogPreviewAfterMaskSensitiveInfoDoesNotExposeCredential(t *testing.T) {
	withDebugEnabled(t, false)
	token := strings.Repeat("A", 3000)
	content := "upstream failed Authorization: Bearer " + token

	preview := LocalLogPreview(MaskSensitiveInfo(content))

	require.NotContains(t, preview, token)
	require.NotContains(t, preview, strings.Repeat("A", 64))
	require.Contains(t, preview, "***")
}
