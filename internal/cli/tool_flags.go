package cli

import "strings"

func toolFlagNames(name, typ string) (base string, negative string) {
	prefix := ""
	if isReservedToolFlagName(name) {
		prefix = "tool-"
	}

	base = "--" + prefix + name
	if typ == "boolean" && !strings.HasPrefix(name, "no-") {
		negative = "--" + prefix + "no-" + name
	}
	return base, negative
}
