package code

// Stable wire values; keep persisted bytes compatible.
const (
	statusOK                   = "OK"
	statusMissing              = "MISSING"
	statusMismatch             = "MISMATCH"
	DimensionCooldown          = "COOLDOWN"
	DimensionTargetLimit       = "TARGET_LIMIT"
	DimensionIPLimit           = "IP_LIMIT"
	DimensionTargetVerifyLimit = "TARGET_VERIFY_LIMIT"
	quotaDayLayout             = "20060102"
	codeKeyPrefix              = "code:"
	cooldownKeyPrefix          = "cooldown:"
	quotaKeyPrefix             = "quota:"
	quotaTargetSegment         = ":target:"
	quotaIPSegment             = ":ip:"
	verifyFailureKeyPrefix     = "verify_failure:"
)

const challengeKeyPrefix = "challenge:"
const challengeHMACDomain = "challenge:"
