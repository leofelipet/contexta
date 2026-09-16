package migrations

import "embed"

// FS contains the versioned database migrations shipped with the binary.
//
//go:embed *.sql
var FS embed.FS
