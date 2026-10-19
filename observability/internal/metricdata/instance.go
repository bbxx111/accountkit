package metricdata

import "errors"

// ValidateInstance enforces the fixed, bounded public adapter-label syntax.
func ValidateInstance(instance string) error {
	if len(instance) == 0 || len(instance) > 64 {
		return errors.New("observability: instance must match [A-Za-z0-9][A-Za-z0-9._-]{0,63}")
	}
	for i := range len(instance) {
		c := instance[i]
		if c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || i > 0 && (c == '.' || c == '_' || c == '-') {
			continue
		}
		return errors.New("observability: instance must match [A-Za-z0-9][A-Za-z0-9._-]{0,63}")
	}
	return nil
}
