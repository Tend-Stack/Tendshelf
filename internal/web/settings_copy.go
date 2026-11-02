package web

// Interface copy for Profile, Settings and Change password (P1b-7). Strings from the design board
// and the copy deck are verbatim; the others follow the voice rules in BRAND.md.
const (
	msgNotCurrentPassword = "That's not your current password."
	msgPasswordChanged    = "Your password is changed. Your other devices are signed out."
	msgSettingInvalid     = "Tendshelf can't save that setting. Reload the page and try again."
	msgThisBrowser        = "This device"
	msgNotUsedYet         = "Not used yet"
	msgUnknownDevice      = "A web browser"
)

// roleLabel is the role as the Profile chip shows it.
func roleLabel(role string) string {
	switch role {
	case "owner":
		return "Owner"
	case "admin":
		return "Admin"
	case "guest":
		return "Guest"
	}
	return "Member"
}
