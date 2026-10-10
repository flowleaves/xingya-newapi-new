package console_setting

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// TestValidateAnnouncementsAcceptsPopupFlag pins the contract that lets an operator mark
// an announcement for the blocking dialog.
//
// GetAnnouncements returns the stored objects verbatim, so the popup flag only has to
// survive validation to reach the frontend. A validator that rejected it — or silently
// stripped it — would look exactly like "the operator never ticked the box".
func TestValidateAnnouncementsAcceptsPopupFlag(t *testing.T) {
	require.NoError(t, ValidateConsoleSettings(
		`[{"id":1,"content":"maintenance","publishDate":"2026-05-01T00:00:00Z","type":"warning","popup":true}]`,
		"Announcements",
	))

	// The flag is optional: announcements written before it existed stay valid.
	require.NoError(t, ValidateConsoleSettings(
		`[{"id":1,"content":"maintenance","publishDate":"2026-05-01T00:00:00Z"}]`,
		"Announcements",
	))

	require.NoError(t, ValidateConsoleSettings(
		`[{"id":1,"content":"maintenance","publishDate":"2026-05-01T00:00:00Z","popup":false}]`,
		"Announcements",
	))
}

// TestValidateAnnouncementsRejectsNonBooleanPopup guards against a stringified flag
// slipping through, which the frontend would then compare strictly against `true`.
func TestValidateAnnouncementsRejectsNonBooleanPopup(t *testing.T) {
	for _, popup := range []string{`"true"`, `1`, `null`, `[]`, `{}`} {
		require.Error(t, ValidateConsoleSettings(
			`[{"id":1,"content":"maintenance","publishDate":"2026-05-01T00:00:00Z","popup":`+popup+`}]`,
			"Announcements",
		), "popup=%s must be rejected", popup)
	}
}
