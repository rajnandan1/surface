package surface

import (
	_ "embed"
	"strings"
)

//go:embed VERSION
var versionFile string

var Version = strings.TrimSpace(versionFile)
