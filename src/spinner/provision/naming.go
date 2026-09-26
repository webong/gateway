package provision

import "strings"

func runtimeResourceName(instanceID string) string {
	var name strings.Builder
	name.WriteString("gateway-server-")
	for _, character := range strings.ToLower(instanceID) {
		if (character >= 'a' && character <= 'z') || (character >= '0' && character <= '9') || character == '-' {
			name.WriteRune(character)
		} else {
			name.WriteByte('-')
		}
	}

	return strings.Trim(name.String(), "-")
}

func runtimeLabelValue(value string) string {
	var label strings.Builder
	for _, character := range strings.ToLower(value) {
		if (character >= 'a' && character <= 'z') || (character >= '0' && character <= '9') || character == '-' || character == '_' || character == '.' {
			label.WriteRune(character)
		} else {
			label.WriteByte('-')
		}
		if label.Len() == 63 {
			break
		}
	}
	result := strings.Trim(label.String(), "-_.")
	if result == "" {
		return "gateway"
	}

	return result
}
