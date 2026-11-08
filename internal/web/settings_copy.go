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
		return "Listener"
	}
	return "Member"
}

// Copy of "Your devices" (profile redesign, decision 0021).
const (
	devTitle    = "Your devices"
	devLead     = "Everywhere you are signed in. Signing out of one leaves the others as they are."
	devSignIns  = "%d sign-ins · %s"
	devLineOnly = "Only this device"
	devLineOne  = "This device and 1 other"
	devLineMany = "This device and %d others"
	devOneAria  = "Sign out %s, %s"

	devThisBrowser = "This browser"
	devNameButton  = "Name"
	devNameLabel   = "Device name"
	devNameHintWeb = "Leave it empty to go back to the usual name."
	devNameHintApp = "Up to 64 characters."
	devNameEmpty   = "Give this phone or app a name. It can't be empty."
	devNameTooLong = "Use 64 characters or fewer."
)
