package admin

import "github.com/bbxx111/accountkit/enum"

// Names shared by filter recognition and field-specific execution.
const (
	filterFieldState       = "state"
	filterFieldCreateTime  = "create_time"
	filterFieldPhone       = "identity.phone"
	filterFieldPhonePrefix = "identity.phone_prefix"
	filterFieldPhoneSuffix = "identity.phone_suffix"
	filterFieldEmail       = "identity.email"
	filterFieldEmailPrefix = "identity.email_prefix"
	filterFieldEmailDomain = "identity.email_domain"
)

// Identity field kinds are data belonging to the same field catalog.
var identityFilterKinds = map[string]enum.IdentityKind{
	filterFieldPhone: enum.IdentityPhone, filterFieldPhonePrefix: enum.IdentityPhone, filterFieldPhoneSuffix: enum.IdentityPhone,
	filterFieldEmail: enum.IdentityEmail, filterFieldEmailPrefix: enum.IdentityEmail, filterFieldEmailDomain: enum.IdentityEmail,
}
