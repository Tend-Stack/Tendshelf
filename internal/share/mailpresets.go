package share

import "strings"

// MailPreset pre-fills the email settings for a well-known provider. It only fills in the server, the
// port and the security mode; the person can change every field, and everything is sent by the same
// SMTP code. No provider has its own API here.
type MailPreset struct {
	ID, Name string
	Host     string // for Amazon SES, a template with {region}
	Port     int
	TLS      string
	Username string // a fixed username some providers use, else empty
	Hint     string // where to create the SMTP password, in plain words
}

// SESRegions are the Amazon SES regions the picker offers.
var SESRegions = []string{"us-east-1", "us-east-2", "us-west-1", "us-west-2", "ca-central-1", "eu-west-1", "eu-west-2",
	"eu-west-3", "eu-central-1", "eu-north-1", "eu-south-1", "ap-south-1", "ap-southeast-1", "ap-southeast-2",
	"ap-northeast-1", "ap-northeast-2", "sa-east-1", "me-south-1", "af-south-1"}

// MailPresets is the one table of providers. The last entry is the person's own server.
var MailPresets = []MailPreset{
	{ID: "ses", Name: "Amazon SES", Host: "email-smtp.{region}.amazonaws.com", Port: 587, TLS: TLSStartTLS,
		Hint: "Use the SMTP username and password from the SES console, not your AWS sign-in. The From address must be verified in SES. While your SES account is in the sandbox, it only sends to verified addresses."},
	{ID: "gmail", Name: "Gmail or Google Workspace", Host: "smtp.gmail.com", Port: 587, TLS: TLSStartTLS,
		Hint: "Turn on 2-step verification, then create an app password in your Google account and use it as the password."},
	{ID: "outlook", Name: "Outlook or Microsoft 365", Host: "smtp.office365.com", Port: 587, TLS: TLSStartTLS,
		Hint: "The account must allow SMTP sign-in. Ask your Microsoft 365 admin if sending is refused."},
	{ID: "fastmail", Name: "Fastmail", Host: "smtp.fastmail.com", Port: 465, TLS: TLSImplicit,
		Hint: "Create an app password for SMTP in Fastmail's privacy and security settings."},
	{ID: "icloud", Name: "iCloud Mail", Host: "smtp.mail.me.com", Port: 587, TLS: TLSStartTLS,
		Hint: "Create an app-specific password at account.apple.com and use it as the password."},
	{ID: "zoho", Name: "Zoho Mail", Host: "smtp.zoho.com", Port: 465, TLS: TLSImplicit,
		Hint: "Create an application-specific password in your Zoho account's security settings."},
	{ID: "mailgun", Name: "Mailgun", Host: "smtp.mailgun.org", Port: 587, TLS: TLSStartTLS,
		Hint: "Use the SMTP credentials of your sending domain, from the Mailgun dashboard."},
	{ID: "sendgrid", Name: "SendGrid", Host: "smtp.sendgrid.net", Port: 587, TLS: TLSStartTLS, Username: "apikey",
		Hint: "The username is the word apikey. Create an API key with permission to send mail and use it as the password."},
	{ID: "postmark", Name: "Postmark", Host: "smtp.postmarkapp.com", Port: 587, TLS: TLSStartTLS,
		Hint: "Use the server's API token as both the username and the password."},
	{ID: "brevo", Name: "Brevo", Host: "smtp-relay.brevo.com", Port: 587, TLS: TLSStartTLS,
		Hint: "Create an SMTP key in your Brevo account and use your login with that key as the password."},
	{ID: "own", Name: "My own mail server", Port: 587, TLS: TLSStartTLS,
		Hint: "Enter your server's name and port. A server on this same computer may use no encryption."},
}

// MailPresetByID finds a preset; ok is false for an unknown id.
func MailPresetByID(id string) (MailPreset, bool) {
	for _, p := range MailPresets {
		if p.ID == id {
			return p, true
		}
	}
	return MailPreset{}, false
}

// Filled returns the preset's server name; region fills Amazon SES's template (an unknown region is
// ignored and leaves the server empty, so the person must pick one).
func (p MailPreset) Filled(region string) string {
	if !strings.Contains(p.Host, "{region}") {
		return p.Host
	}
	for _, r := range SESRegions {
		if r == region {
			return strings.ReplaceAll(p.Host, "{region}", region)
		}
	}
	return ""
}
