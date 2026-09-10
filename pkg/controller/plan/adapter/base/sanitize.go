package base

import (
	"regexp"
	"strings"

	k8svalidation "k8s.io/apimachinery/pkg/util/validation"
)

var sanitizeNameRx = regexp.MustCompile(`[^a-zA-Z0-9_.-]+`)

// IsInLabelTags reports whether tagName appears in labelTags (case-insensitive).
func IsInLabelTags(tagName string, labelTags []string) bool {
	for _, lt := range labelTags {
		if strings.EqualFold(tagName, lt) {
			return true
		}
	}
	return false
}

func isValidK8sMetadataValue(s string) bool {
	if s == "" {
		return true
	}
	errs := k8svalidation.IsValidLabelValue(s)
	return len(errs) == 0
}

// SanitizeForK8sMetadata makes a string safe for use as a K8s label key/value.
func SanitizeForK8sMetadata(s string) string {
	if s == "" {
		return ""
	}
	if isValidK8sMetadataValue(s) {
		return s
	}
	sanitized := sanitizeNameRx.ReplaceAllString(s, "_")
	sanitized = strings.Trim(sanitized, "_.-")
	if len(sanitized) > 63 {
		sanitized = sanitized[:63]
		sanitized = strings.TrimRight(sanitized, "_.-")
	}
	return sanitized
}
