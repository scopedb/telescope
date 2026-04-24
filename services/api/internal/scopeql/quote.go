package scopeql

import "strings"

func QuoteStringLiteral(value string) string {
	escaped := strings.ReplaceAll(value, `\`, `\\`)
	escaped = strings.ReplaceAll(escaped, "'", "''")
	return "'" + escaped + "'"
}

func QuoteIdentifierPath(value string) string {
	parts := strings.Split(value, ".")
	for i, part := range parts {
		parts[i] = QuoteIdentifier(part)
	}
	return strings.Join(parts, ".")
}

func QuoteIdentifier(value string) string {
	escaped := strings.ReplaceAll(value, `\`, `\\`)
	escaped = strings.ReplaceAll(escaped, "`", "``")
	return "`" + escaped + "`"
}

func quoteFunctionName(value string) string {
	parts := strings.Split(value, ".")
	for _, part := range parts {
		if !isBareIdentifier(part) {
			return QuoteIdentifierPath(value)
		}
	}
	return value
}

func isBareIdentifier(value string) bool {
	if value == "" {
		return false
	}
	for i, r := range value {
		switch {
		case r == '_':
		case r >= 'a' && r <= 'z':
		case r >= 'A' && r <= 'Z':
		case i > 0 && r >= '0' && r <= '9':
		default:
			return false
		}
	}
	return true
}
