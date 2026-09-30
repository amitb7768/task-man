package transitions

import "embed"

// FS exposes the embedded transition YAML files for runtime loading.
//
//go:embed *.yaml
var FS embed.FS
